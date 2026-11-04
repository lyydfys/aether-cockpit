package kiro

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Kiro 的额度读的是 AWS Q 的 getUsageLimits。三个容易读错的点：
//
//  1. 域名：额度接口在 `q.<region>.amazonaws.com`，不是老的
//     `codewhisperer.*`（那是 CodeWhisperer 时代的历史域名，部分区域直接 404）。
//  2. 参数：必须带 `resourceType=AGENTIC_REQUEST`，否则拿到的是另一套计量。
//  3. 免费试用：试用账号的主额度是 0，真正的额度在 `freeTrialInfo` 里。
//     只读 usageBreakdownList 会让试用账号显示成"没有额度"。
//
// 另外新版响应把额度拆成 usageBreakdowns.{plan,covered,bonus}，老版是
// usageBreakdownList 数组；两种形状都要认，最后还有扁平的 credits 汇总兜底。

// kiroQuotaHost 按区域给出额度接口域名。
//
// GovCloud 用 q-fips 前缀（普通域在 gov 分区不通），其余用 q.<region>。
func kiroQuotaHost(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		region = defaultRegion
	}
	if strings.HasPrefix(region, "us-gov-") {
		return "https://q-fips." + region + ".amazonaws.com"
	}
	return "https://q." + region + ".amazonaws.com"
}

// kiroNum 是"可能缺失的数字"：上游两种精度字段并存，缺字段与 0 必须区分。
type kiroNum struct {
	Val   float64
	Valid bool
}

func (n *kiroNum) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		return nil
	}
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		return nil // 未知形态当"缺失"，不让整个响应解析失败
	}
	n.Val = v
	n.Valid = true
	return nil
}

// kiroPick 返回第一个有效的读数（*WithPrecision 永远排在前）。
func kiroPick(ns ...kiroNum) kiroNum {
	for _, n := range ns {
		if n.Valid {
			return n
		}
	}
	return kiroNum{}
}

// kiroBreakdown 是一个额度分组（agentic requests / code completions / 试用）。
type kiroBreakdown struct {
	ResourceType string     `json:"resourceType"`
	UsageLimit   kiroNum    `json:"usageLimit"`
	UsageLimitP  kiroNum    `json:"usageLimitWithPrecision"`
	CurrentUsage kiroNum    `json:"currentUsage"`
	CurrentP     kiroNum    `json:"currentUsageWithPrecision"`
	FreeTrial    *kiroTrial `json:"freeTrialInfo"`
}

// kiroTrial 是免费试用的额度（与主额度分属两个池）。
type kiroTrial struct {
	UsageLimit   kiroNum `json:"usageLimit"`
	UsageLimitP  kiroNum `json:"usageLimitWithPrecision"`
	CurrentUsage kiroNum `json:"currentUsage"`
	CurrentP     kiroNum `json:"currentUsageWithPrecision"`
	Expiry       any     `json:"freeTrialExpiry"`
}

// kiroUsage 是 getUsageLimits 的响应（新旧两种形状都收）。
type kiroUsage struct {
	DaysUntilReset int64 `json:"daysUntilReset"`
	NextDateReset  any   `json:"nextDateReset"`
	Subscription   struct {
		Title string `json:"subscriptionTitle"`
		Type  string `json:"type"`
	} `json:"subscriptionInfo"`
	BreakdownList []kiroBreakdown `json:"usageBreakdownList"`
	Breakdowns    *struct {
		Plan    *kiroBreakdown `json:"plan"`
		Covered *kiroBreakdown `json:"covered"`
		Bonus   *kiroBreakdown `json:"bonus"`
	} `json:"usageBreakdowns"`
	// 新版还有一个扁平的 credits 汇总。
	Credits *struct {
		Total kiroNum `json:"total"`
		Used  kiroNum `json:"used"`
	} `json:"credits"`
}

// QueryQuota 读取 Kiro 订阅的用量上限。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token := strings.TrimSpace(firstNonEmpty(env.GetCredential("access_token"), env.GetCredential("token")))
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Kiro 账号缺少 access_token，无法读取用量").WithProvider("kiro")
	}
	profileArn := strings.TrimSpace(env.GetCredential("profile_arn"))
	if profileArn == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Kiro 账号缺少 profile_arn，无法读取用量").WithProvider("kiro")
	}
	region := strings.TrimSpace(env.GetCredential("region"))
	if r := regionFromProfileArn(profileArn); r != "" {
		region = r
	}
	if region == "" {
		region = defaultRegion
	}

	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = kiroQuotaHost(region)
	}
	// resourceType 决定读哪套计量：Kiro 按 agentic requests 计费。
	endpoint := base + "/getUsageLimits?origin=AI_EDITOR&resourceType=AGENTIC_REQUEST&profileArn=" +
		url.QueryEscape(profileArn)

	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Kiro 用量请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if strings.HasPrefix(token, "ksk_") {
		req.Header.Set("tokentype", "API_KEY")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AetherCockpit/quota")

	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"读取 Kiro 用量失败："+err.Error()).WithProvider("kiro")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 403 + BANNED 是账号封禁：终态，重试没有意义，也不该继续轮询。
		if resp.StatusCode == http.StatusForbidden && strings.Contains(strings.ToUpper(string(raw)), "BANNED") {
			return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
				"Kiro 账号已被封禁（BANNED），请停止使用该账号").WithProvider("kiro")
		}
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"读取 Kiro 用量失败："+mapHTTPError(resp.StatusCode, string(raw), resp.Header).Message).WithProvider("kiro")
	}

	var wire kiroUsage
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Kiro 用量回复解析失败："+err.Error()).WithProvider("kiro")
	}

	resetAt := parseResetSeconds(wire.NextDateReset)
	q := &provider.QuotaStatus{ReadAt: time.Now().Unix(), Plan: wire.Subscription.Title, ExpiresAt: resetAt}

	var remaining float64
	addBreakdown := func(b kiroBreakdown, aside bool, name string) {
		limit := kiroPick(b.UsageLimitP, b.UsageLimit)
		used := kiroPick(b.CurrentP, b.CurrentUsage)
		if !limit.Valid || limit.Val <= 0 {
			return
		}
		u := 0.0
		if used.Valid {
			u = used.Val
		}
		if name == "" {
			name = breakdownName(b.ResourceType)
		}
		q.Windows = append(q.Windows, provider.QuotaWindow{
			Name: name, Amount: int64(u), Limit: int64(limit.Val),
			Unit: "credits", ResetsAt: resetAt, Used: clampPct(u / limit.Val * 100), Aside: aside,
		})
		remaining += math.Max(limit.Val-u, 0)
	}

	for _, b := range wire.BreakdownList {
		addBreakdown(b, false, "")
		// 免费试用额度是独立钱包：主额度为 0 的试用账号全靠它。
		if b.FreeTrial == nil {
			continue
		}
		limit := kiroPick(b.FreeTrial.UsageLimitP, b.FreeTrial.UsageLimit)
		used := kiroPick(b.FreeTrial.CurrentP, b.FreeTrial.CurrentUsage)
		if !limit.Valid || limit.Val <= 0 {
			continue
		}
		u := 0.0
		if used.Valid {
			u = used.Val
		}
		trialReset := resetAt
		if ts := parseResetSeconds(b.FreeTrial.Expiry); ts > 0 {
			trialReset = ts
		}
		q.Windows = append(q.Windows, provider.QuotaWindow{
			Name: "免费试用", Amount: int64(u), Limit: int64(limit.Val),
			Unit: "credits", ResetsAt: trialReset, Used: clampPct(u / limit.Val * 100), Aside: true,
		})
		remaining += math.Max(limit.Val-u, 0)
	}
	if wire.Breakdowns != nil {
		for _, b := range []*kiroBreakdown{wire.Breakdowns.Plan, wire.Breakdowns.Covered, wire.Breakdowns.Bonus} {
			if b != nil {
				addBreakdown(*b, false, "")
			}
		}
	}
	// 新版扁平汇总：前面两种形状都没给出数据时用它兜底。
	if len(q.Windows) == 0 && wire.Credits != nil && wire.Credits.Total.Valid && wire.Credits.Total.Val > 0 {
		u := 0.0
		if wire.Credits.Used.Valid {
			u = wire.Credits.Used.Val
		}
		q.Windows = append(q.Windows, provider.QuotaWindow{
			Name: "Credits", Amount: int64(u), Limit: int64(wire.Credits.Total.Val),
			Unit: "credits", ResetsAt: resetAt, Used: clampPct(u / wire.Credits.Total.Val * 100),
		})
		remaining += math.Max(wire.Credits.Total.Val-u, 0)
	}
	if len(q.Windows) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Kiro 用量回复里没有可读的额度（usageBreakdownList / usageBreakdowns / credits 都为空）").WithProvider("kiro")
	}
	q.Balance = remaining
	q.BalanceUnit = "credits"
	return q, nil
}

// clampPct 把百分比夹到 0-100。
func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func breakdownName(resourceType string) string {
	switch strings.ToUpper(resourceType) {
	case "AGENTIC_REQUEST":
		return "Agentic requests"
	case "CODE_COMPLETIONS":
		return "Code completions"
	}
	if resourceType == "" {
		return "Credits"
	}
	return resourceType
}

// parseResetSeconds 接受 epoch 秒/毫秒与 RFC3339 字符串。
func parseResetSeconds(v any) int64 {
	switch x := v.(type) {
	case float64:
		if x > 1e12 {
			return int64(x / 1000)
		}
		if x > 1e9 {
			return int64(x)
		}
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0
		}
		for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05Z"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.Unix()
			}
		}
	}
	return 0
}

var _ provider.QuotaCapability = (*Provider)(nil)
