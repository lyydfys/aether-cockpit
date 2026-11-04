package relaybilling

// 中转面板的自动识别。
//
// 背景：中转站几乎都是 relay-panel / relay-panel / relay-panel / relay-panel 这一系的分叉，
// 但它们「用 API Key 读余额」的暴露面各不相同，而我们过去只试探了 OpenAI
// 的 /dashboard/billing/*，读不到就把 404 原样抛给用户 —— 用户看到的
// "额度读取失败" 里，其实有一半是「这个站有别的接口，我们没试」。
//
// 参考实现（local-vault / local-provider-switch）的做法一致：**多候选逐个试**，
// 每个候选带自己的单位换算，404 continue、401 判失效、429 退避。
// 这里按同样的思路实现，并额外用 /api/status 把汇率读出来，
// 避免把 relay-panel 的 "额度单位整数" 直接当成美元。
//
// 关键单位规则（社区事实标准）：
//   - relay-panel / relay-panel：quota、used_quota 是「额度单位」，默认 500000 = 1 美元，
//     面板可通过 /api/status 的 quota_per_unit 改这个比例。
//   - OpenAI billing usage：total_usage 是「美分」，÷100 = 美元。
//   - 其余（balance / remaining / credits / available_*）：原样即美元。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// defaultQuotaPerUnit 是 relay-panel 系的默认换算比例（500000 单位 = 1 美元）。
const defaultQuotaPerUnit = 500000.0

// panelInfo 是从 /api/status 读到的面板信息。
type panelInfo struct {
	// Kind 目前只用来记日志，不参与分支（统一按 relay-panel 语义解析）。
	Kind string
	// QuotaPerUnit 是「额度单位 → 美元」的除数。
	QuotaPerUnit float64
	// DisplayInCurrency 表示面板本身就按货币显示（有的分叉会改语义）。
	DisplayInCurrency bool
	// DisplayType 是面板声明的显示口径（USD / CNY / TOKENS）。
	//
	// 为什么要读它：relay-panel 允许站点把额度显示成人民币甚至 token 数，
	// 这时把 quota/500000 当美元解释就是错的。读不到就按 USD 处理，
	// 但文案里要标出这是默认假设。
	DisplayType string
}

// detectPanel 用 /api/status 判断这是不是 relay-panel/relay-panel 一系，并读出汇率。
//
// 很多面板会把这个接口开成公开的，所以不需要 Key；读不到就按默认汇率算。
func detectPanel(ctx context.Context, client *http.Client, root, key string) panelInfo {
	info := panelInfo{QuotaPerUnit: defaultQuotaPerUnit}
	var st struct {
		Data struct {
			QuotaPerUnit      float64 `json:"quota_per_unit"`
			DisplayInCurrency bool    `json:"display_in_currency"`
			QuotaDisplayType  string  `json:"quota_display_type"`
			Version           string  `json:"version"`
		} `json:"data"`
		QuotaPerUnit      float64 `json:"quota_per_unit"`
		DisplayInCurrency bool    `json:"display_in_currency"`
		QuotaDisplayType  string  `json:"quota_display_type"`
		Version           string  `json:"version"`
	}
	if err := getJSON(ctx, client, root+"/api/status", key, &st); err != nil {
		// 也试 /v1 下的同一路径：少数分叉把它挂在 /v1 后面。
		if err2 := getJSON(ctx, client, root+"/v1/api/status", key, &st); err2 != nil {
			return info
		}
	}
	if st.Data.QuotaPerUnit > 0 {
		info.QuotaPerUnit = st.Data.QuotaPerUnit
	} else if st.QuotaPerUnit > 0 {
		info.QuotaPerUnit = st.QuotaPerUnit
	}
	info.DisplayInCurrency = st.Data.DisplayInCurrency || st.DisplayInCurrency
	if t := strings.TrimSpace(st.Data.QuotaDisplayType); t != "" {
		info.DisplayType = strings.ToUpper(t)
	} else if t := strings.TrimSpace(st.QuotaDisplayType); t != "" {
		info.DisplayType = strings.ToUpper(t)
	}
	info.Kind = "relay-panel"
	return info
}

// relayPanelUserDoc 是 /api/user/self 的响应形状（多取几个可能的分叉字段）。
type relayPanelUserDoc struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Quota       *float64 `json:"quota"`
		UsedQuota   *float64 `json:"used_quota"`
		RemainQuota *float64 `json:"remain_quota"`
		Group       string   `json:"group"`
		Username    string   `json:"username"`
	} `json:"data"`
	// 少数分叉把这几个字段平铺在顶层。
	Quota       *float64 `json:"quota"`
	UsedQuota   *float64 `json:"used_quota"`
	RemainQuota *float64 `json:"remain_quota"`
}

// queryRelayPanelUser 读 relay-panel 系的用户额度。
//
// 注意鉴权：这些面板的 /api/user/self 通常要**会话或用户级 access token**，
// 而不是 sk- 形式的模型 Key；部分分叉接受 sk- 但需要 Relay-Panel-User 头。
// 所以这里按「有没有 access_token / user id」决定带哪些头，能不能读通
// 由上游回答，不做假设。
func queryRelayPanelUser(ctx context.Context, client *http.Client, root, token, userID string, info panelInfo) (*quotaResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/api/user/self", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AetherCockpit/quota")
	// 面板要求：Relay-Panel-User 头写用户 id。用户 id 只能来自账号配置，
	// 从 Key 里猜不出来 —— 拿不到就不带，让上游自己判。
	if uid := strings.TrimSpace(userID); uid != "" {
		req.Header.Set("Relay-Panel-User", uid)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := readLimited(resp.Body)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &statusError{Status: resp.StatusCode, Body: "鉴权被拒（该接口需要账号 access token，不是模型 Key）"}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &statusError{Status: resp.StatusCode, Body: ""}
	}
	var doc relayPanelUserDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, &statusError{Status: resp.StatusCode, Body: "返回不是 JSON（该路径不是额度接口）"}
	}
	// relay-panel 的 /api/user/self 未登录时返回 **200 + success:false**，
	// 只看状态码会误判成"读到了但没数据"。这里按 success/message 归类。
	if !doc.Success && strings.TrimSpace(doc.Message) != "" {
		if isAuthMessage(doc.Message) {
			return nil, &statusError{Status: resp.StatusCode, Body: "鉴权被拒（该接口需要账号 access token，不是模型 Key）"}
		}
		return nil, &statusError{Status: resp.StatusCode, Body: "面板拒绝：" + doc.Message}
	}
	remaining := firstFloat(doc.Data.Quota, doc.Data.RemainQuota, doc.Quota, doc.RemainQuota)
	used := firstFloat(doc.Data.UsedQuota, doc.UsedQuota)
	if remaining == nil && used == nil {
		return nil, &statusError{Status: resp.StatusCode, Body: "响应里没有 quota 字段"}
	}
	unit := info.QuotaPerUnit
	if unit <= 0 {
		unit = defaultQuotaPerUnit
	}
	res := &quotaResult{UnitUSD: true}
	if remaining != nil {
		res.Balance = *remaining / unit
		res.HasBalance = true
	}
	if used != nil {
		res.UsedUSD = *used / unit
		res.HasUsed = true
	}
	if doc.Data.Group != "" {
		res.Plan = doc.Data.Group
	}
	switch {
	case res.HasBalance && res.HasUsed:
		res.Source = "面板额度（剩余 + 已用）"
	case res.HasBalance:
		res.Source = "面板额度（剩余）"
	default:
		res.Source = "面板额度（仅已用）"
	}
	// 显示口径不是美元时（CNY / TOKENS），明确标出来：把人民币数当美元
	// 展示，比不展示更糟。
	switch info.DisplayType {
	case "", "USD":
	default:
		res.Source += "（面板口径 " + info.DisplayType + "，单位未经换算）"
	}
	return res, nil
}

// quotaResult 是面板读取的中间结果，由调用方转成 provider.QuotaStatus。
type quotaResult struct {
	Balance    float64
	HasBalance bool
	UsedUSD    float64
	HasUsed    bool
	UnitUSD    bool
	Plan       string
	Source     string
}

// firstFloat 返回第一个非 nil 指针的值。
func firstFloat(vs ...*float64) *float64 {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

// isAuthMessage 判断面板的失败文案是不是"缺凭证"（各分叉的中英文写法）。
func isAuthMessage(msg string) bool {
	m := strings.ToLower(msg)
	for _, kw := range []string{"未登录", "无权", "令牌", "token", "unauthor", "forbidden", "access denied", "登录"} {
		if strings.Contains(m, kw) {
			return true
		}
	}
	return false
}

// readLimited 读响应体，限制大小（额度接口不该返回大文件）。
func readLimited(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, 256<<10))
}
