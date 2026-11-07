package trae

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Trae 的"额度"有两个互不相干的东西，早期我们把它们搞混了：
//
//	1. 签到积分（/ug/checkin_credits/status）：每天签到攒的额外积分，
//	   是"额外奖励"，不是套餐额度。
//	2. 套餐额度（/pay/ide_user_ent_usage）：订阅里真正被消耗的用量，
//	   字段是 basic_usage_amount（已用）与 basic_usage_limit（总量）。
//
// 用量卡要显示的是 2；1 只该留在签到卡片上。之前读的是 1，于是用户看到的
// "剩余积分"是签到累计值，与套餐实际剩余完全对不上。

// traeQuotaHostsForTest 让测试把额度根地址换成本地服务器；
// 生产代码始终按 region 拼域名。
var traeQuotaHostsForTest []string

// traeQuotaHosts 返回该账号可用的额度接口根地址。
//
// 国际版与国内版是**不同域名**，但接口路径同构；solo 变体沿用所在区域的域名。
func (p *Provider) traeQuotaHosts() []string {
	if len(traeQuotaHostsForTest) > 0 {
		return traeQuotaHostsForTest
	}
	if p.region == "CN" {
		return []string{"https://api.trae.cn"}
	}
	return []string{"https://api.trae.ai"}
}

// Trae 套餐额度的响应形状（2026-11-07 实测）。
//
// v2 `/trae/api/v2/pay/ide_user_ent_usage` 是**扁平结构**，真正的套餐额度在顶层
// usage_summary：
//
//	{"is_credits_billing":true,
//	 "usage_summary":{"consumed_amount":212.74,"total_amount":1400,"consumption_ratio":0.152},
//	 "user_entitlement_pack_list":[{"display_desc":"签到奖励","usage":{"credits_amount":100}}, ...]}
//
// pack 里的 quota 只有 credits_limit（签到/赠送包），basic_usage_limit 恒为 0。
// 旧实现只认 basic_usage_limit，于是在 v2 上判定"没有额度包"，退回签到积分（100）——
// 这就是卡片显示 100 而不是 1400-212.74 的原因。
//
// v1 是嵌套结构（data.user_entitlement_pack_list[].entitlement_base_info.quota.
// basic_usage_limit + usage.basic_usage_amount），保留作为老账号兜底。

// traeUsageSummary 是 v2 顶层额度汇总。
type traeUsageSummary struct {
	ConsumedAmount   float64 `json:"consumed_amount"`
	TotalAmount      float64 `json:"total_amount"`
	ConsumptionRatio float64 `json:"consumption_ratio"`
}

// traePackQuota 是 pack 里的额度声明。
type traePackQuota struct {
	BasicUsageLimit float64 `json:"basic_usage_limit"`
	BonusUsageLimit float64 `json:"bonus_usage_limit"`
	CreditsLimit    float64 `json:"credits_limit"`
}

// traePackUsage 是 pack 的已用量。
type traePackUsage struct {
	BasicUsageAmount float64 `json:"basic_usage_amount"`
	BonusUsageAmount float64 `json:"bonus_usage_amount"`
	CreditsAmount    float64 `json:"credits_amount"`
}

// traePackBaseInfo 是 pack 的基础信息（含到期时间）。
type traePackBaseInfo struct {
	Quota   traePackQuota `json:"quota"`
	EndTime flexUnix      `json:"end_time"`
}

// traeEntPack 是一个额度包。
type traeEntPack struct {
	DisplayDesc string           `json:"display_desc"`
	BaseInfo    traePackBaseInfo `json:"entitlement_base_info"`
	Usage       traePackUsage    `json:"usage"`
}

// traeEntUsage 兼容 v1（data 包裹）与 v2（顶层扁平）两种形状。
type traeEntUsage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`

	// v2 顶层
	IsCreditsBilling bool              `json:"is_credits_billing"`
	UsageSummary     *traeUsageSummary `json:"usage_summary"`
	PacksV2          []traeEntPack     `json:"user_entitlement_pack_list"`

	// v1 嵌套
	Data struct {
		Packs []traeEntPack `json:"user_entitlement_pack_list"`
	} `json:"data"`
}

// packs 返回两种形状里的 pack 列表（v2 优先）。
func (u traeEntUsage) packs() []traeEntPack {
	if len(u.PacksV2) > 0 {
		return u.PacksV2
	}
	return u.Data.Packs
}

// summary 返回 v2 的顶层汇总（v1 没有）。
func (u traeEntUsage) summary() *traeUsageSummary {
	if u.UsageSummary == nil || u.UsageSummary.TotalAmount <= 0 {
		return nil
	}
	return u.UsageSummary
}

// packsTotal 汇总 pack 里的额度（v1 口径）。
//
// 只统计有 basic_usage_limit 的包：签到奖励包给的是 credits_limit，含义是
// "这个包最多兑这么多积分"，与套餐额度是两套东西，加进来会把 1400 撑成 2000+。
func (u traeEntUsage) packsTotal() (total, used float64, resetAt int64, ok bool) {
	for _, pk := range u.packs() {
		limit := pk.BaseInfo.Quota.BasicUsageLimit
		if limit <= 0 {
			continue
		}
		ok = true
		total += limit
		used += pk.Usage.BasicUsageAmount
		if ts := int64(pk.BaseInfo.EndTime); ts > 0 && (resetAt == 0 || ts < resetAt) {
			resetAt = ts
		}
	}
	return
}

// latestEnd 返回所有 pack 里最晚的到期时间（套餐按月续，取最早会显示成某个签到包）。
func (u traeEntUsage) latestEnd() int64 {
	var out int64
	for _, pk := range u.packs() {
		if ts := int64(pk.BaseInfo.EndTime); ts > out {
			out = ts
		}
	}
	return out
}

// flexUnix 解析"秒 / 毫秒 / RFC3339 / 数字字符串"四种写法。
//
// 上游不同版本混用：v2 给秒级数字，v1 给 RFC3339，个别老接口给毫秒。
// 用普通 int64 字段会解析失败变 0，界面上就是"没有重置时间"。
type flexUnix int64

func (f *flexUnix) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(strings.Trim(string(b), "\""))
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil && n > 0 {
		if n > 1e12 { // 毫秒
			n /= 1000
		}
		*f = flexUnix(int64(n))
		return nil
	}
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if ts, err := time.Parse(layout, s); err == nil {
			*f = flexUnix(ts.Unix())
			return nil
		}
	}
	*f = 0
	return nil
}

// QueryQuota 读取 Trae 的套餐额度用量。
//
// 顺序：套餐额度 → 签到积分兜底。套餐接口读不到时（老账号/接口变更）
// 至少还能显示签到积分，而不是一片空白。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token := accessToken(env)
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Trae 账号缺少 token；请重新导入登录态").WithProvider(p.id)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	q, err := p.queryEntUsage(ctx, env, token)
	if err == nil && q != nil {
		return q, nil
	}
	// 套餐接口失败不直接报错：签到积分仍可展示，把失败原因放进文案。
	if q2, err2 := p.queryCheckinCredits(ctx, env, token); err2 == nil {
		if err != nil {
			q2.Message = "套餐额度读取失败（" + err.Error() + "），以下为签到积分"
		}
		return q2, nil
	}
	if err == nil {
		err = fmt.Errorf("响应里没有额度包")
	}
	return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Trae 额度读取失败："+err.Error()).WithProvider(p.id)
}

// queryEntUsage 读套餐额度。CN 先试 v2 再回落 v1（国际版同构）。
func (p *Provider) queryEntUsage(ctx context.Context, env *provider.AccountEnv, token string) (*provider.QuotaStatus, error) {
	var lastErr error
	for _, host := range p.traeQuotaHosts() {
		for _, ver := range []string{"v2", "v1"} {
			endpoint := host + "/trae/api/" + ver + "/pay/ide_user_ent_usage"
			var out traeEntUsage
			if err := p.postJSON(ctx, env, token, endpoint, `{"require_usage":true}`, &out); err != nil {
				lastErr = err
				continue
			}
			if out.Code != 0 {
				lastErr = fmt.Errorf("接口返回 %d：%s", out.Code, strings.TrimSpace(out.Message))
				continue
			}
			if q := entUsageToQuota(out); q != nil {
				return q, nil
			}
			lastErr = fmt.Errorf("响应里没有额度包")
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的额度接口")
	}
	return nil, lastErr
}

// entUsageToQuota 把额度包列表换算成"总量/已用/剩余"。
//
// 上游只给 limit 与 amount，不给剩余，所以剩余靠减法并夹到 0；
// 多个包（月付 + 加量）按"总量相加、已用相加"合并，重置时间取最早的一个。
// entUsageToQuota 把额度响应换算成 QuotaStatus。
//
// 两代口径优先级：
//  1. v2 顶层 usage_summary（total_amount / consumed_amount）——实测 Trae CN 的真实
//     套餐额度只在这里（1400 / 212.74），pack 里的 basic_usage_limit 恒为 0；
//  2. v1 的 pack 列表（basic_usage_limit + basic_usage_amount）；
//  3. 都没有则返回 nil，让调用方退回签到积分。
func entUsageToQuota(out traeEntUsage) *provider.QuotaStatus {
	var total, used float64
	var resetAt int64

	if sum := out.summary(); sum != nil {
		total = sum.TotalAmount
		used = sum.ConsumedAmount
		resetAt = out.latestEnd()
	} else {
		t, u, at, ok := out.packsTotal()
		if !ok {
			return nil
		}
		total, used, resetAt = t, u, at
	}
	if total <= 0 {
		return nil
	}
	if used < 0 {
		used = 0
	}
	if used > total {
		used = total
	}
	remain := total - used

	q := &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Total:       int(total),
		Used:        int(used),
		Remaining:   int(remain),
		Unit:        "credits",
		Balance:     remain,
		BalanceUnit: "credits",
		Until:       resetAt,
		ExpiresAt:   resetAt,
	}
	q.Windows = append(q.Windows, provider.QuotaWindow{
		Name:     "套餐额度",
		Used:     used * 100 / total,
		Limit:    int64(total),
		Amount:   int64(used),
		Unit:     "credits",
		ResetsAt: resetAt,
	})
	return q
}

// queryCheckinCredits 是兜底：签到累计积分（不是套餐额度）。
func (p *Provider) queryCheckinCredits(ctx context.Context, env *provider.AccountEnv, token string) (*provider.QuotaStatus, error) {
	var st traeCheckinStatus
	if err := p.checkinCall(ctx, env, token, "/status", &st); err != nil {
		return nil, err
	}
	if st.Code != 0 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Trae 签到接口返回 %d：%s", st.Code, st.Message).WithProvider(p.id)
	}
	// 只取 credits：参考实现里没有 extra_credits，凭空相加会放大数字。
	credits := st.Credits
	q := &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Balance:     credits,
		BalanceUnit: "credits",
		Message:     "签到积分（套餐额度不可读）",
	}
	if !st.Enable && credits == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeNoProvider, "Trae 没有返回积分信息").WithProvider(p.id)
	}
	return q, nil
}

// postJSON 发一个带 Bearer 的 JSON POST 并解析。
func (p *Provider) postJSON(ctx context.Context, env *provider.AccountEnv, token, endpoint, body string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	p.applyHeaders(req.Header, env, token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := env.Client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("登录态已失效，请重新登录")
	case http.StatusForbidden:
		return fmt.Errorf("无权访问该接口（可能需要企业权限）")
	case http.StatusNotFound:
		return fmt.Errorf("该接口不存在")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("响应无法解析")
	}
	return nil
}

// parseTraeTime 解析 Trae 的时间：既可能是 RFC3339 字符串，也可能是秒级时间戳。
func parseTraeTime(v string) int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.Unix()
	}
	if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
		return t.Unix()
	}
	var n int64
	if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
		if n > 1_000_000_000_000 {
			return n / 1000
		}
		return n
	}
	return 0
}
