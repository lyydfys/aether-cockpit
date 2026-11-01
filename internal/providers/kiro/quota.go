package kiro

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// QueryQuota 读取 Kiro 订阅的用量上限（AWS CodeWhisperer getUsageLimits）。
//
// 同一份订阅可能同时给出"agentic requests"与"code completions"两条
// usageBreakdown；这里逐条折算成百分比窗口，并把剩余额度折进 Balance。
// 端点读不到时如实报错，不影响对话路由。
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
		base = "https://codewhisperer." + region + ".amazonaws.com"
	}
	endpoint := base + "/getUsageLimits?origin=AI_EDITOR&profileArn=" + url.QueryEscape(profileArn)

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
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"读取 Kiro 用量失败："+mapHTTPError(resp.StatusCode, string(raw), resp.Header).Message).WithProvider("kiro")
	}

	var wire struct {
		DaysUntilReset int64 `json:"daysUntilReset"`
		NextDateReset  any   `json:"nextDateReset"`
		Subscription   struct {
			Title string `json:"subscriptionTitle"`
			Type  string `json:"type"`
		} `json:"subscriptionInfo"`
		UsageBreakdownList []struct {
			ResourceType  string  `json:"resourceType"`
			CurrentUsage  float64 `json:"currentUsage"`
			CurrentPrec   float64 `json:"currentUsageWithPrecision"`
			UsageLimit    float64 `json:"usageLimit"`
			UsageLimitPre float64 `json:"usageLimitWithPrecision"`
		} `json:"usageBreakdownList"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Kiro 用量回复解析失败："+err.Error()).WithProvider("kiro")
	}

	resetAt := parseResetSeconds(wire.NextDateReset)
	q := &provider.QuotaStatus{ReadAt: time.Now().Unix(), Plan: wire.Subscription.Title, ExpiresAt: resetAt}
	var remaining float64
	for _, b := range wire.UsageBreakdownList {
		used := b.CurrentPrec
		if used <= 0 {
			used = b.CurrentUsage
		}
		limit := b.UsageLimitPre
		if limit <= 0 {
			limit = b.UsageLimit
		}
		name := breakdownName(b.ResourceType)
		w := provider.QuotaWindow{
			Name: name, Amount: int64(used), Limit: int64(limit),
			Unit: "credits", ResetsAt: resetAt,
		}
		if limit > 0 {
			w.Used = used / limit * 100
			if w.Used > 100 {
				w.Used = 100
			}
			if limit-used > 0 {
				remaining += limit - used
			}
		}
		q.Windows = append(q.Windows, w)
	}
	if len(q.Windows) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Kiro 用量回复里没有 usageBreakdownList").WithProvider("kiro")
	}
	q.Balance = remaining
	q.BalanceUnit = "credits"
	return q, nil
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

// 编译期确认实现的能力。
var _ provider.QuotaCapability = (*Provider)(nil)
