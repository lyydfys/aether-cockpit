package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 厂商余额接口（与 cockpit-tools / 各家控制台同源）：
//
//	DeepSeek   GET /user/balance                    -> balance_infos[{currency,total_balance,…}]
//	Moonshot   GET /v1/users/me/balance             -> data{available_balance,voucher_balance,cash_balance}
//	OpenRouter GET /api/v1/credits                  -> data{total_credits,total_usage}
//
// 都从账号 base_url 推导 origin（自建代理 / 假上游可测），Bearer 账号 Key。

func quotaOrigin(base, fallback string) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		b = fallback
	}
	if u, err := url.Parse(b); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Scheme + "://" + u.Host
	}
	return strings.TrimRight(fallback, "/")
}

func quotaKey(env *provider.AccountEnv) string {
	key := strings.TrimSpace(env.GetCredential("api_key"))
	if key == "" {
		key = strings.TrimSpace(env.GetCredential("access_token"))
	}
	return key
}

func quotaGet(ctx context.Context, env *provider.AccountEnv, endpoint, providerID string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeInternal, "构造余额请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+quotaKey(env))
	req.Header.Set("Accept", "application/json")
	resp, err := env.Client().Do(req)
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "余额接口连接失败").
			WithCause(err).WithProvider(providerID).WithRetryable(true)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "余额接口 HTTP %d：%s",
			resp.StatusCode, truncateStr(strings.TrimSpace(string(raw)), 200)).WithProvider(providerID)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "余额接口返回无法解析").WithCause(err).WithProvider(providerID)
	}
	return nil
}

func queryDeepSeekQuota(ctx context.Context, env *provider.AccountEnv, defaultBase, providerID string) (*provider.QuotaStatus, error) {
	if quotaKey(env) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "DeepSeek 账号缺少 API Key").WithProvider(providerID)
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var payload struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency string `json:"currency"`
			Total    string `json:"total_balance"`
			Granted  string `json:"granted_balance"`
			ToppedUp string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := quotaGet(ctx, env, quotaOrigin(env.GetCredential("base_url"), defaultBase)+"/user/balance", providerID, &payload); err != nil {
		return nil, err
	}
	q := &provider.QuotaStatus{ReadAt: time.Now().Unix(), BalanceUnit: "CNY", Unit: "CNY"}
	if len(payload.BalanceInfos) == 0 {
		if payload.IsAvailable {
			q.Message = "DeepSeek 未返回余额明细"
			return q, nil
		}
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "DeepSeek 账户当前不可用").WithProvider(providerID)
	}
	first := payload.BalanceInfos[0]
	total, _ := strconv.ParseFloat(first.Total, 64)
	q.Balance = total
	if first.Currency != "" {
		q.BalanceUnit = strings.ToUpper(first.Currency)
		q.Unit = q.BalanceUnit
	}
	q.Message = fmt.Sprintf("DeepSeek 余额（赠送 %s / 充值 %s）", first.Granted, first.ToppedUp)
	if !payload.IsAvailable {
		q.Message = "DeepSeek 账户不可用 · " + q.Message
	}
	return q, nil
}

func queryMoonshotQuota(ctx context.Context, env *provider.AccountEnv, defaultBase, providerID string) (*provider.QuotaStatus, error) {
	if quotaKey(env) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Kimi 账号缺少 API Key").WithProvider(providerID)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = defaultBase
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var payload struct {
		Code   int  `json:"code"`
		Status bool `json:"status"`
		Data   struct {
			Available float64 `json:"available_balance"`
			Voucher   float64 `json:"voucher_balance"`
			Cash      float64 `json:"cash_balance"`
		} `json:"data"`
	}
	if err := quotaGet(ctx, env, quotaOrigin(base, defaultBase)+"/v1/users/me/balance", providerID, &payload); err != nil {
		return nil, err
	}
	if payload.Code != 0 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Kimi 余额接口返回 code=%d", payload.Code).WithProvider(providerID)
	}
	unit := "CNY"
	if providerID == "moonshot" && !strings.Contains(base, "moonshot.cn") {
		unit = "USD"
	}
	return &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Balance:     payload.Data.Available,
		BalanceUnit: unit,
		Unit:        unit,
		Message:     fmt.Sprintf("Kimi 余额（代金券 %.2f / 现金 %.2f）", payload.Data.Voucher, payload.Data.Cash),
	}, nil
}

func querySiliconFlowQuota(ctx context.Context, env *provider.AccountEnv, defaultBase, providerID string) (*provider.QuotaStatus, error) {
	if quotaKey(env) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "SiliconFlow 账号缺少 API Key").WithProvider(providerID)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = defaultBase
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var payload struct {
		Code   int  `json:"code"`
		Status bool `json:"status"`
		Data   struct {
			Balance       string `json:"balance"`
			ChargeBalance string `json:"chargeBalance"`
			TotalBalance  string `json:"totalBalance"`
		} `json:"data"`
	}
	if err := quotaGet(ctx, env, quotaOrigin(base, defaultBase)+"/v1/user/info", providerID, &payload); err != nil {
		return nil, err
	}
	if payload.Code != 0 && payload.Code != 20000 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "SiliconFlow 余额接口返回 code=%d", payload.Code).WithProvider(providerID)
	}
	total, _ := strconv.ParseFloat(payload.Data.TotalBalance, 64)
	granted, _ := strconv.ParseFloat(payload.Data.Balance, 64)
	charge, _ := strconv.ParseFloat(payload.Data.ChargeBalance, 64)
	unit := "CNY"
	if strings.Contains(base, "siliconflow.com") {
		unit = "USD"
	}
	return &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Balance:     total,
		BalanceUnit: unit,
		Unit:        unit,
		Message:     fmt.Sprintf("SiliconFlow 余额（赠金 %.2f / 充值 %.2f）", granted, charge),
	}, nil
}

func queryOpenRouterQuota(ctx context.Context, env *provider.AccountEnv, defaultBase, providerID string) (*provider.QuotaStatus, error) {
	if quotaKey(env) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "OpenRouter 账号缺少 API Key").WithProvider(providerID)
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var payload struct {
		Data struct {
			TotalCredits float64 `json:"total_credits"`
			TotalUsage   float64 `json:"total_usage"`
		} `json:"data"`
	}
	if err := quotaGet(ctx, env, quotaOrigin(env.GetCredential("base_url"), defaultBase)+"/api/v1/credits", providerID, &payload); err != nil {
		return nil, err
	}
	remaining := payload.Data.TotalCredits - payload.Data.TotalUsage
	if remaining < 0 {
		remaining = 0
	}
	return &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Balance:     remaining,
		BalanceUnit: "USD",
		Unit:        "USD",
		Message:     fmt.Sprintf("OpenRouter 额度（已用 $%.2f / 总额 $%.2f）", payload.Data.TotalUsage, payload.Data.TotalCredits),
	}, nil
}
