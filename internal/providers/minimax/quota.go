package minimax

// MiniMax Code 的额度：账号的 M Plan / 积分余额。
//
// 与签到同一套签名请求（mcode 0.6.12）：
//   - POST /matrix/api/v1/user/get_user_extra_info  → 工作区列表（取 workspace_type 0）
//   - POST /matrix/api/v1/commerce/get_membership_info {workspace_id}
//     → has_token_plan / token_plan_tier / token_plan_expires_at /
//       op_credit_summary.total_remaining_amount / opcredit_balance
// 编码套餐的逐窗口用量（M Plan 的 5h/周）需要 platform 域的另一组接口，
// 当前只呈现余额与套餐到期时间；拿不到就如实返回错误，不编造窗口。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token := strings.TrimSpace(accessToken(env))
	if token == "" {
		return nil, fmt.Errorf("MiniMax Code 账号缺少 access token")
	}
	uid := p.userID(env)
	if uid == "" {
		uid = p.identity(ctx, env, token)
	}
	if uid == "" {
		return nil, fmt.Errorf("MiniMax 额度读取需要 user_id（账号信息里没有 realUserID）")
	}

	wsID, err := p.workspaceID(ctx, env, token, uid)
	if err != nil {
		return nil, err
	}
	body := ""
	if wsID != "" {
		body = fmt.Sprintf(`{"workspace_id":%s}`, wsID)
	} else {
		body = "{}"
	}
	raw, err := p.accountCall(ctx, env, "/matrix/api/v1/commerce/get_membership_info", token, uid, body)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("MiniMax membership 返回不是 JSON")
	}
	if d, ok := root["data"].(map[string]any); ok {
		for k, v := range d {
			if _, exists := root[k]; !exists {
				root[k] = v
			}
		}
	}
	q := &provider.QuotaStatus{ReadAt: time.Now().Unix()}
	if s := firstString(root, "token_plan_tier"); s != "" {
		q.Plan = s
	}
	if n, ok := firstNumber(root, "token_plan_expires_at"); ok && n > 0 {
		q.Until = normalizeUnix(n)
	}
	if sum, ok := root["op_credit_summary"].(map[string]any); ok {
		if v, ok := firstNumber(sum, "total_remaining_amount"); ok {
			q.Balance = v
			q.BalanceUnit = "积分"
		}
	}
	if q.Balance == 0 {
		if v, ok := firstNumber(root, "opcredit_balance"); ok {
			q.Balance = v
			q.BalanceUnit = "积分"
		}
	}
	if has, ok := root["has_token_plan"].(bool); ok && has && q.Plan == "" {
		q.Plan = "M Plan"
	}
	group := firstString(root, "op_group_id")
	q.Windows = p.planWindows(ctx, env, token, group)
	if cw := p.creditWindow(ctx, env, token, uid); cw != nil {
		// 有 M Plan 时积分是旁边的兜底（与 aether 一致）。
		cw.Aside = q.Plan != ""
		q.Windows = append(q.Windows, *cw)
	}
	if q.Plan == "" && q.Balance == 0 && q.Until == 0 && len(q.Windows) == 0 {
		return nil, fmt.Errorf("MiniMax 账号没有可读的套餐或积分")
	}
	return q, nil
}

// workspaceID 取账号自己的 workspace（workspace_type 0），返回 JSON 数字或带引号字符串。
func (p *Provider) workspaceID(ctx context.Context, env *provider.AccountEnv, token, uid string) (string, error) {
	raw, err := p.accountCall(ctx, env, "/matrix/api/v1/user/get_user_extra_info", token, uid, "{}")
	if err != nil {
		return "", err
	}
	var root struct {
		Workspaces []map[string]any `json:"workspaces"`
		Data       struct {
			Workspaces []map[string]any `json:"workspaces"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("MiniMax workspace 返回不是 JSON")
	}
	list := root.Workspaces
	if list == nil {
		list = root.Data.Workspaces
	}
	for _, w := range list {
		if t, ok := firstNumber(w, "workspace_type"); ok && t != 0 {
			continue
		}
		if v, ok := w["workspace_id"]; ok {
			switch x := v.(type) {
			case float64:
				return strconv.FormatInt(int64(x), 10), nil
			case string:
				if strings.TrimSpace(x) != "" {
					return strconv.Quote(x), nil
				}
			}
		}
	}
	return "", nil
}

// accountCall 是签名 JSON 请求，返回原始响应体。
func (p *Provider) accountCall(ctx context.Context, env *provider.AccountEnv, path, token, uid, body string) ([]byte, error) {
	req, err := p.signedRequest(ctx, env, http.MethodPost, path, token, uid, body)
	if err != nil {
		return nil, err
	}
	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("MiniMax 登录态已失效，请重新登录")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MiniMax %s: HTTP %d", path, resp.StatusCode)
	}
	var envl checkinEnvelope
	_ = json.Unmarshal(raw, &envl)
	if envl.StatusInfo != nil && envl.StatusInfo.Code != 0 {
		if envl.StatusInfo.Code == 1000048 {
			return nil, fmt.Errorf("MiniMax 登录态已失效，请重新登录")
		}
		return nil, fmt.Errorf("MiniMax: %s", envl.StatusInfo.Message)
	}
	if envl.BaseResp != nil && envl.BaseResp.Code != 0 {
		return nil, fmt.Errorf("MiniMax: %s", envl.BaseResp.Msg)
	}
	return raw, nil
}

func firstString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func firstNumber(m map[string]any, key string) (float64, bool) {
	switch v := m[key].(type) {
	case float64:
		return v, true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func normalizeUnix(n float64) int64 {
	v := int64(n)
	if v > 1e12 {
		return v / 1000
	}
	return v
}

// planRaw 与 JSON 字段一一对应（Go 的嵌入标签不便于同名解析，直接平铺）。
type planRaw struct {
	ModelName      string   `json:"model_name"`
	IntervalPct    *float64 `json:"current_interval_remaining_percent"`
	IntervalStatus *int     `json:"current_interval_status"`
	IntervalStart  *float64 `json:"start_time"`
	IntervalEnd    *float64 `json:"end_time"`
	IntervalTotal  *float64 `json:"current_interval_total_count"`
	// current_interval_usage_count 的字面意思是"使用数"，但上游语义是
	// **剩余数**（参考实现里有单测钉住这一点）。这是最容易写反的字段：
	// 当成已用会让"还剩 90%"显示成"已用 90%"。
	IntervalUsedCount *float64 `json:"current_interval_usage_count"`
	WeeklyPct         *float64 `json:"current_weekly_remaining_percent"`
	WeeklyStatus      *int     `json:"current_weekly_status"`
	WeeklyStart       *float64 `json:"weekly_start_time"`
	WeeklyEnd         *float64 `json:"weekly_end_time"`
	WeeklyTotal       *float64 `json:"current_weekly_total_count"`
	WeeklyUsedCount   *float64 `json:"current_weekly_usage_count"`
}

// effectivePct 在"只有计数、没有百分比"时按 剩余/总量 反算百分比。
//
// 上游有时只下发 count 字段（`*_usage_count` 是**剩余**），没有百分比；
// 这时如果直接跳过窗口，用户就看不到额度了。
func effectivePct(pct, remainCount, total *float64) *float64 {
	if pct != nil {
		return pct
	}
	if remainCount == nil || total == nil || *total <= 0 {
		return nil
	}
	v := *remainCount / *total * 100
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return &v
}

// planWindows 读取 M Plan 的 5 小时/周窗口（aether planWindows 的等价实现）。
func (p *Provider) planWindows(ctx context.Context, env *provider.AccountEnv, token, group string) []provider.QuotaWindow {
	base := p.profile.platformBase
	if v := strings.TrimRight(strings.TrimSpace(env.GetCredential("platform_base_url")), "/"); v != "" {
		base = v
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/api/openplatform/coding_plan/remains", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if group != "" {
		req.Header.Set("X-Group-Id", group)
	}
	resp, err := env.Client().Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var body struct {
		BaseResp *struct {
			Code int    `json:"status_code"`
			Msg  string `json:"status_msg"`
		} `json:"base_resp"`
		ModelRemains []planRaw `json:"model_remains"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil
	}
	if body.BaseResp != nil && body.BaseResp.Code != 0 {
		return nil
	}
	toMs := func(v float64) int64 {
		if v <= 0 {
			return 0
		}
		if v < 1e12 {
			return int64(v * 1000)
		}
		return int64(v)
	}
	num := func(v *float64) float64 {
		if v == nil {
			return 0
		}
		return *v
	}
	numi := func(v *int) int {
		if v == nil {
			return 0
		}
		return *v
	}
	zero := func(v *float64) bool { return num(v) == 0 }
	var out []provider.QuotaWindow
	for _, k := range body.ModelRemains {
		name := strings.TrimSpace(k.ModelName)
		if name == "" || (numi(k.IntervalStatus) == 3 && numi(k.WeeklyStatus) == 3 && zero(k.IntervalTotal) && zero(k.WeeklyTotal)) {
			continue
		}
		general := strings.EqualFold(name, "general")
		add := func(leftPct *float64, status int, start, end float64, week bool) {
			if status == 3 || (leftPct == nil && status != 2) {
				return
			}
			used := 50.0
			if status == 2 {
				used = 100
			} else {
				used = clampUsed(100 - num(leftPct))
			}
			w := provider.QuotaWindow{Used: used, Aside: !general}
			span := int64(0)
			startMs, endMs := toMs(start), toMs(end)
			if startMs > 0 && endMs > startMs {
				span = endMs - startMs
			} else if week {
				span = 7 * 86400 * 1000
			}
			switch {
			case week:
				w.Name = "7 days"
			case span > 86400_000 && span%86400_000 == 0:
				w.Name = fmt.Sprintf("%d days", span/86400_000)
			case span >= 3600_000 && span%3600_000 == 0:
				w.Name = fmt.Sprintf("%d hours", span/3600_000)
			case span > 0:
				w.Name = fmt.Sprintf("%d minutes", span/60_000)
			default:
				w.Name = "Allowance"
			}
			if endMs > 0 {
				w.ResetsAt = endMs / 1000
			}
			if startMs > 0 {
				w.StartsAt = startMs / 1000
			}
			if !general {
				w.Name = strings.ToUpper(name[:1]) + name[1:] + " · " + w.Name
			}
			out = append(out, w)
		}
		add(effectivePct(k.IntervalPct, k.IntervalUsedCount, k.IntervalTotal),
			numi(k.IntervalStatus), num(k.IntervalStart), num(k.IntervalEnd), false)
		add(effectivePct(k.WeeklyPct, k.WeeklyUsedCount, k.WeeklyTotal),
			numi(k.WeeklyStatus), num(k.WeeklyStart), num(k.WeeklyEnd), true)
	}
	return out
}

func clampUsed(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// creditBatch 是积分明细里的一批积分。
type creditBatch struct {
	Type      int     `json:"credit_type"`
	Granted   *string `json:"granted_amount"`
	Remaining *string `json:"remaining_amount"`
	ExpireMS  float64 `json:"expire_at_ms"`
}

type creditPage struct {
	Details    []creditBatch `json:"details"`
	TotalCount int           `json:"total_count"`
	Data       *struct {
		Details    []creditBatch `json:"details"`
		TotalCount int           `json:"total_count"`
	} `json:"data"`
}

func creditAmount(v *string) (float64, bool) {
	if v == nil {
		return 0, false
	}
	t := strings.ReplaceAll(strings.TrimSpace(*v), ",", "")
	if t == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// creditWindow 把未过期的积分批次合成一个 Credits 窗口（aether creditsWindow）。
func (p *Provider) creditWindow(ctx context.Context, env *provider.AccountEnv, token, uid string) *provider.QuotaWindow {
	var batches []creditBatch
	for page := 1; page <= 10; page++ {
		req, err := p.signedRequestExtra(ctx, env, http.MethodGet, "/minimax-cloud/api/v1/credit/details", token, uid, "", [][2]string{
			{"page_number", strconv.Itoa(page)}, {"page_size", "20"}, {"include_expired", "true"},
		})
		if err != nil {
			return nil
		}
		resp, err := env.Client().Do(req)
		if err != nil {
			return nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil
		}
		var cp creditPage
		if json.Unmarshal(raw, &cp) != nil {
			return nil
		}
		list, total := cp.Details, cp.TotalCount
		if cp.Data != nil {
			list, total = cp.Data.Details, cp.Data.TotalCount
		}
		if list == nil && total == 0 {
			return nil
		}
		batches = append(batches, list...)
		if len(batches) >= total || len(list) == 0 {
			break
		}
	}
	now := time.Now().UnixMilli()
	var total, left float64
	var soonest int64
	for _, b := range batches {
		if b.Type != 1 && b.Type != 2 {
			continue
		}
		granted, ok1 := creditAmount(b.Granted)
		rest, ok2 := creditAmount(b.Remaining)
		if !ok1 || !ok2 || b.ExpireMS <= 0 {
			return nil
		}
		exp := int64(b.ExpireMS)
		if exp <= now {
			continue
		}
		if rest < 0 {
			rest = 0
		}
		total += granted
		if rest > granted {
			rest = granted
		}
		left += rest
		if rest > 0 && (soonest == 0 || exp < soonest) {
			soonest = exp
		}
	}
	if total <= 0 {
		return nil
	}
	used := total - left
	if used < 0 {
		used = 0
	}
	w := &provider.QuotaWindow{
		Name: "Credits", Used: used / total * 100,
		Amount: int64(used + 0.5), Limit: int64(total + 0.5), Unit: "credits",
	}
	if soonest > 0 {
		w.ResetsAt = soonest / 1000
	}
	return w
}
