// Package relaybilling 读取"中继/聚合面板"账号的余额。
//
// Codex CLI 的中转、openai-compat 自定义端点大多是 relay-panel / relay-panel 一类面板，
// 它们保留 OpenAI 的账单接口：
//
//	GET {base}/dashboard/billing/subscription → {hard_limit_usd, unlimited_quota,…}
//	GET {base}/dashboard/billing/usage?start_date&end_date → {total_usage（美分）}
//
// 读得到就换算成"剩余 $X"；读不到（401/404 等）如实报错，绝不编造额度。
package relaybilling

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Query 读一个 OpenAI 兼容中继账号的余额。
func Query(ctx context.Context, env *provider.AccountEnv, baseURL string) (*provider.QuotaStatus, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "该账号没有可用的 Base URL，无法读取余额").WithProvider(env.ProviderID)
	}
	key := strings.TrimSpace(env.GetCredential("api_key"))
	if key == "" {
		key = strings.TrimSpace(env.GetCredential("access_token"))
	}
	if key == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "账号缺少 api_key，无法读取余额").WithProvider(env.ProviderID)
	}

	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client := env.Client()

	roots := billingRoots(base)

	var sub struct {
		HardLimitUSD     float64 `json:"hard_limit_usd"`
		SoftLimitUSD     float64 `json:"soft_limit_usd"`
		SystemHardLimit  float64 `json:"system_hard_limit_usd"`
		UnlimitedQuota   bool    `json:"unlimited_quota"`
		AccessUntil      int64   `json:"access_until"`
		HasPaymentMethod *bool   `json:"has_payment_method"`
	}
	var usage struct {
		TotalUsage float64 `json:"total_usage"` // 美分
	}

	var lastErr string
	okSub := false
	for _, root := range roots {
		if err := getJSON(ctx, client, root+"/dashboard/billing/subscription", key, &sub); err != nil {
			lastErr = err.Error()
			continue
		}
		okSub = true
		break
	}
	if !okSub {
		// 不是所有面板都实现 /dashboard/billing/*。aether 认识的两个兜底：
		// OpenAI 的 credit_grants（美元）与 relay-panel 的 /api/usage/token
		// （额度单位，500000 = 1 美元）。
		if bal, source, ok := fallbackBalance(ctx, client, roots, key); ok {
			return &provider.QuotaStatus{
				ReadAt:      nowSeconds(),
				Balance:     bal,
				BalanceUnit: "USD",
				Message:     source,
			}, nil
		}
		// 最后一层：面板自身接口（relay-panel 系 /api/user/self）。
		// 放在 billing 之后，因为它通常要用户级 access token 而不是模型 Key；
		// 有就读，没有就让上游把结论带回来。
		if q, cerr := panelQuota(ctx, env, client, roots, key); cerr == nil {
			return q, nil
		} else if strings.Contains(cerr.Error(), "鉴权被拒") {
			// 面板接口明确拒绝：这个站是面板类中转，但 Key 不是用户级令牌。
			// 这种情况要给可执行的提示，而不是一句 404。
			return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
				"该站是面板类中转，但 /api/user/self 需要账号 access token（不是模型 Key）；"+
					"可在账号里填 access_token 与用户 id，或改用自定义余额接口").WithProvider(env.ProviderID)
		}
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"该渠道没有账单接口（也没有可读的面板额度接口）："+lastErr).WithProvider(env.ProviderID)
	}

	now := time.Now()
	since := now.AddDate(0, 0, -30)
	query := "?start_date=" + since.Format("2006-01-02") + "&end_date=" + now.Format("2006-01-02")
	okUsage := false
	for _, root := range roots {
		if err := getJSON(ctx, client, root+"/dashboard/billing/usage"+query, key, &usage); err != nil {
			lastErr = err.Error()
			continue
		}
		okUsage = true
		break
	}

	limit := sub.HardLimitUSD
	if limit <= 0 {
		limit = sub.SystemHardLimit
	}
	if limit <= 0 {
		limit = sub.SoftLimitUSD
	}
	q := &provider.QuotaStatus{ReadAt: now.Unix(), BalanceUnit: "USD"}
	if sub.UnlimitedQuota || limit <= 0 {
		q.Message = "中继未返回额度上限（按量计费，余额以面板为准）"
		if okUsage {
			q.Message = "近 30 天已用 $" + formatUSD(usage.TotalUsage/100)
		}
		return q, nil
	}
	used := usage.TotalUsage / 100
	if used < 0 {
		used = 0
	}
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	q.Balance = remaining
	q.Message = "剩余 $" + formatUSD(remaining) + " / 上限 $" + formatUSD(limit)
	return q, nil
}

// fallbackBalance 依次尝试 credit_grants 与 relay-panel 的 token 用量接口。
// 返回 (美元余额, 来源说明, 是否成功)。
func fallbackBalance(ctx context.Context, client *http.Client, roots []string, key string) (float64, string, bool) {
	var grants struct {
		TotalAvailable float64 `json:"total_available"`
		TotalGranted   float64 `json:"total_granted"`
		TotalUsed      float64 `json:"total_used"`
	}
	for _, root := range roots {
		if err := getJSON(ctx, client, root+"/dashboard/billing/credit_grants", key, &grants); err != nil {
			continue
		}
		bal := grants.TotalAvailable
		if bal < 0 {
			bal = 0
		}
		return bal, "剩余 $" + formatUSD(bal) + "（面板信贷）", true
	}
	var token struct {
		TotalAvailable float64 `json:"total_available"`
		TotalGranted   float64 `json:"total_granted"`
		TotalUsed      float64 `json:"total_used"`
	}
	for _, root := range roots {
		if err := getJSON(ctx, client, root+"/api/usage/token", key, &token); err != nil {
			continue
		}
		bal := token.TotalAvailable / 500000
		if bal < 0 {
			bal = 0
		}
		return bal, "剩余 $" + formatUSD(bal) + "（Key 额度）", true
	}
	// relay-panel 一类的中转站把 /api/usage/token/ 作为唯一的 sk- 可读额度接口。
	//
	// 注意结尾的斜杠：relay-panel 的路由是 `/api/usage/token/`，少了斜杠会 301/404，
	// 这是我们过去"读不到额度"的一个真实原因，两种写法都试。
	tokenPaths := []string{"/api/usage/token/", "/api/usage/token"}
	for _, p := range tokenPaths {
		for _, root := range roots {
			var tok struct {
				TotalAvailable float64 `json:"total_available"`
				TotalUsed      float64 `json:"total_used"`
				Unlimited      bool    `json:"unlimited_quota"`
				// relay-panel 在 allow_user_quota_query 开启时顺带返回账号级余额。
				UserQuota *float64 `json:"user_quota"`
			}
			if err := getJSON(ctx, client, root+p, key, &tok); err != nil {
				continue
			}
			if tok.Unlimited {
				return 0, "该 Key 无额度上限（站内标注不限量）", true
			}
			if tok.UserQuota != nil && *tok.UserQuota > 0 {
				bal := *tok.UserQuota / defaultQuotaPerUnit
				return bal, "剩余 $" + formatUSD(bal) + "（账号级额度）", true
			}
			if tok.TotalAvailable == 0 && tok.TotalUsed == 0 {
				continue
			}
			bal := tok.TotalAvailable / defaultQuotaPerUnit
			if bal < 0 {
				bal = 0
			}
			return bal, "剩余 $" + formatUSD(bal) + "（Key 额度）", true
		}
	}
	// relay-panel 一类的面板在 /v1/usage 直接给 remaining（美元）。
	var usage struct {
		Remaining *float64 `json:"remaining"`
		Balance   *float64 `json:"balance"`
	}
	for _, root := range roots {
		if err := getJSON(ctx, client, root+"/v1/usage", key, &usage); err != nil {
			continue
		}
		bal := 0.0
		switch {
		case usage.Remaining != nil:
			bal = *usage.Remaining
		case usage.Balance != nil:
			bal = *usage.Balance
		default:
			continue
		}
		if bal < 0 {
			bal = 0
		}
		return bal, "剩余 $" + formatUSD(bal), true
	}
	return 0, "", false
}

func nowSeconds() int64 { return time.Now().Unix() }

// billingRoots 给出账单接口可能挂载的根路径：
// 有的面板挂在 /v1 下，有的挂在站点根；账号填的 base_url 两种都可能。
func billingRoots(base string) []string {
	out := make([]string, 0, 3)
	add := func(v string) {
		v = strings.TrimRight(strings.TrimSpace(v), "/")
		if v == "" {
			return
		}
		for _, x := range out {
			if x == v {
				return
			}
		}
		out = append(out, v)
	}
	add(base)
	if strings.HasSuffix(base, "/v1") {
		add(strings.TrimSuffix(base, "/v1"))
	} else {
		add(base + "/v1")
	}
	return out
}

func getJSON(ctx context.Context, client *http.Client, url, key string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AetherCockpit/quota")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &statusError{Status: resp.StatusCode, Body: string(raw)}
	}
	// 很多中转面板会把未知路径回落成首页 HTML；直接说清楚，
	// 比把 "invalid character '<'" 抛给用户有用。
	if t := strings.TrimSpace(string(raw)); strings.HasPrefix(t, "<") {
		return &statusError{Status: resp.StatusCode, Body: "返回 HTML 页面（该路径不是账单接口）"}
	}
	return json.Unmarshal(raw, dst)
}

type statusError struct {
	Status int
	Body   string
}

func (e *statusError) Error() string {
	msg := strings.TrimSpace(e.Body)
	if len(msg) > 120 {
		msg = msg[:120]
	}
	if msg == "" {
		return "HTTP " + http.StatusText(e.Status)
	}
	return "HTTP " + http.StatusText(e.Status) + "：" + msg
}

func formatUSD(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	return s
}
