package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Cline 的余额与 ClinePass 窗口来自账号接口（与控制台同源），API Key 直接 Bearer：
//
//	GET /api/v1/users/me                   -> {id, email, …}
//	GET /api/v1/users/{id}/balance         -> {balance, userId}（百万分之一积分，1 积分 = 1 美元）
//	GET /api/v1/users/me/plan/usage-limits -> {limits:[{type,percentUsed,resetsAt?}]}
//
// 响应包在 {success,error,data} 信封里；未订阅套餐时 usage-limits 返回 404。

var errClineNoPlan = errors.New("cline: no plan")

func queryClineQuota(ctx context.Context, env *provider.AccountEnv, defaultBase, providerID string) (*provider.QuotaStatus, error) {
	key := strings.TrimSpace(env.GetCredential("api_key"))
	if key == "" {
		key = strings.TrimSpace(env.GetCredential("access_token"))
	}
	if key == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Cline 账号缺少 API Key").
			WithProvider(providerID)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = strings.TrimRight(defaultBase, "/")
	}
	origin := base
	if u, err := url.Parse(base); err == nil && u.Scheme != "" && u.Host != "" {
		origin = u.Scheme + "://" + u.Host
	}
	c := &clineClient{ctx: ctx, client: env.Client(), key: key, origin: origin, providerID: providerID}

	var me struct {
		ID string `json:"id"`
	}
	if err := c.get("/api/v1/users/me", &me, false); err != nil {
		return nil, err
	}
	if strings.TrimSpace(me.ID) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Cline 账号接口没有返回用户 ID").
			WithProvider(providerID)
	}
	var bal struct {
		Balance float64 `json:"balance"`
	}
	if err := c.get("/api/v1/users/"+url.PathEscape(me.ID)+"/balance", &bal, false); err != nil {
		return nil, err
	}
	// 百万分之一积分 → 美元，保留 4 位小数（与 Cline 的展示精度一致）。
	usd := math.Round(bal.Balance/100) / 10000

	q := &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Balance:     usd,
		BalanceUnit: "USD",
		Unit:        "USD",
		Message:     "Cline 积分余额",
	}
	var limits struct {
		Limits []struct {
			Type     string  `json:"type"`
			Percent  float64 `json:"percentUsed"`
			ResetsAt string  `json:"resetsAt"`
		} `json:"limits"`
	}
	if err := c.get("/api/v1/users/me/plan/usage-limits", &limits, true); err != nil && !errors.Is(err, errClineNoPlan) {
		return nil, err
	}
	names := map[string]string{"five_hour": "5 小时", "weekly": "7 天", "monthly": "30 天"}
	for _, l := range limits.Limits {
		name, ok := names[strings.ToLower(strings.TrimSpace(l.Type))]
		if !ok {
			continue
		}
		w := provider.QuotaWindow{Name: name, Used: l.Percent, Limit: 100, Unit: "%"}
		if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(l.ResetsAt)); err == nil {
			w.ResetsAt = t.Unix()
		}
		q.Windows = append(q.Windows, w)
	}
	if len(q.Windows) > 0 {
		q.Message = "Cline 积分余额 · ClinePass 窗口"
	}
	return q, nil
}

type clineClient struct {
	ctx        context.Context
	client     *http.Client
	key        string
	origin     string
	providerID string
}

func (c *clineClient) get(path string, out any, allow404 bool) error {
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, c.origin+path, nil)
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeInternal, "构造 Cline 额度请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Cline 额度接口连接失败").
			WithCause(err).WithProvider(c.providerID).WithRetryable(true)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound && allow404 {
		return errClineNoPlan
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Cline 额度接口 HTTP %d：%s",
			resp.StatusCode, truncateStr(strings.TrimSpace(string(raw)), 200)).WithProvider(c.providerID)
	}
	var envelope struct {
		Success bool            `json:"success"`
		Error   string          `json:"error"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if !envelope.Success && envelope.Error != "" {
			return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Cline 额度接口返回错误："+envelope.Error).
				WithProvider(c.providerID)
		}
		return json.Unmarshal(envelope.Data, out)
	}
	return json.Unmarshal(raw, out)
}
