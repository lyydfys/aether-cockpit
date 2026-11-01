package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Command Code 的积分与滚动窗口来自账号接口（与官方 CLI 同源）：
//
//	GET /alpha/whoami                     -> {org?:{id}}
//	GET /alpha/billing/credits[?orgId=]   -> {credits:{monthlyCredits,purchasedCredits,freeCredits},
//	                                          windowLimits:{limited,fiveHour:{used,cap,resetAt},weekly:{…}}}
//	GET /alpha/billing/subscriptions      -> {data:{planId,currentPeriodStart,currentPeriodEnd}}
//	GET /alpha/usage/summary?since=…      -> {totalCost,totalMonthlyCredits}
//
// 积分单位是美元；窗口 resetAt 是 epoch 毫秒；组织账户才带 orgId（个人账户带空值会 400）。
// 月度窗口上游只给「剩余」，上限按本周期已用 + 剩余还原。

type ccNumber float64

func (n *ccNumber) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*n = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return err
		}
		*n = ccNumber(v)
		return nil
	}
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*n = ccNumber(v)
	return nil
}

type ccWindow struct {
	Used    ccNumber `json:"used"`
	Cap     ccNumber `json:"cap"`
	ResetAt ccNumber `json:"resetAt"`
}

type ccCredits struct {
	Monthly   ccNumber `json:"monthlyCredits"`
	Purchased ccNumber `json:"purchasedCredits"`
	Free      ccNumber `json:"freeCredits"`
}

type ccCreditsPayload struct {
	Credits      *ccCredits `json:"credits"`
	WindowLimits struct {
		Limited  bool      `json:"limited"`
		FiveHour *ccWindow `json:"fiveHour"`
		Weekly   *ccWindow `json:"weekly"`
	} `json:"windowLimits"`
}

var ccPlanNames = map[string]string{
	"individual-go": "Go", "individual-goat": "GOAT", "individual-pro": "Pro",
	"individual-pro-v1": "Pro", "individual-provider": "Provider",
	"individual-max": "Max", "individual-ultra": "Ultra", "teams-pro": "Teams Pro",
}

func queryCommandCodeQuota(ctx context.Context, env *provider.AccountEnv, defaultBase, providerID string) (*provider.QuotaStatus, error) {
	key := strings.TrimSpace(env.GetCredential("api_key"))
	if key == "" {
		key = strings.TrimSpace(env.GetCredential("access_token"))
	}
	if key == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Command Code 账号缺少 API Key").
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
	c := &ccClient{ctx: ctx, client: env.Client(), key: key, origin: origin, providerID: providerID}

	var who struct {
		Org *struct {
			ID string `json:"id"`
		} `json:"org"`
	}
	if err := c.get("/alpha/whoami", nil, &who); err != nil {
		return nil, err
	}
	q := url.Values{}
	if who.Org != nil && strings.TrimSpace(who.Org.ID) != "" {
		q.Set("orgId", strings.TrimSpace(who.Org.ID))
	}
	var credits ccCreditsPayload
	if err := c.get("/alpha/billing/credits", q, &credits); err != nil {
		return nil, err
	}
	if credits.Credits == nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Command Code 额度接口没有返回 credits").
			WithProvider(providerID)
	}
	total := float64(credits.Credits.Monthly + credits.Credits.Purchased + credits.Credits.Free)
	qStatus := &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Balance:     total,
		BalanceUnit: "USD",
		Unit:        "USD",
		Message:     "Command Code 积分（包月 + 充值 + 赠送）",
	}
	addWindow := func(name string, w *ccWindow) {
		if w == nil || w.Cap <= 0 {
			return
		}
		percent := float64(w.Used) / float64(w.Cap) * 100
		if percent < 0 {
			percent = 0
		}
		qStatus.Windows = append(qStatus.Windows, provider.QuotaWindow{
			Name: name, Used: percent, Limit: 100, Unit: "%",
			ResetsAt: int64(w.ResetAt) / 1000,
		})
	}
	if credits.WindowLimits.Limited {
		addWindow("5 小时", credits.WindowLimits.FiveHour)
		addWindow("7 天", credits.WindowLimits.Weekly)
	}

	// 订阅周期与月度窗口：拿不到就只展示积分，不影响主读数。
	var sub struct {
		Data struct {
			PlanID             string `json:"planId"`
			CurrentPeriodStart string `json:"currentPeriodStart"`
			CurrentPeriodEnd   string `json:"currentPeriodEnd"`
		} `json:"data"`
	}
	if err := c.get("/alpha/billing/subscriptions", q, &sub); err == nil {
		if plan := strings.TrimSpace(sub.Data.PlanID); plan != "" {
			if name, ok := ccPlanNames[plan]; ok {
				qStatus.Plan = name
			} else {
				qStatus.Plan = plan
			}
		}
		if start := strings.TrimSpace(sub.Data.CurrentPeriodStart); start != "" {
			summaryQ := url.Values{}
			for k, v := range q {
				summaryQ[k] = v
			}
			summaryQ.Set("since", start)
			var summary struct {
				TotalCost           *ccNumber `json:"totalCost"`
				TotalMonthlyCredits *ccNumber `json:"totalMonthlyCredits"`
			}
			if err := c.get("/alpha/usage/summary", summaryQ, &summary); err == nil {
				used := 0.0
				if summary.TotalMonthlyCredits != nil {
					used = float64(*summary.TotalMonthlyCredits)
				} else if summary.TotalCost != nil {
					used = float64(*summary.TotalCost)
				}
				if limit := used + float64(credits.Credits.Monthly); limit > 0 {
					w := provider.QuotaWindow{Name: "30 天", Used: used / limit * 100, Limit: 100, Unit: "%"}
					if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(sub.Data.CurrentPeriodEnd)); err == nil {
						w.ResetsAt = t.Unix()
					}
					qStatus.Windows = append(qStatus.Windows, w)
				}
			}
		}
	}
	return qStatus, nil
}

type ccClient struct {
	ctx        context.Context
	client     *http.Client
	key        string
	origin     string
	providerID string
}

func (c *ccClient) get(path string, query url.Values, out any) error {
	target := c.origin + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(c.ctx, http.MethodGet, target, nil)
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeInternal, "构造 Command Code 额度请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Command Code 额度接口连接失败").
			WithCause(err).WithProvider(c.providerID).WithRetryable(true)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Command Code 额度接口 HTTP %d：%s",
			resp.StatusCode, truncateStr(strings.TrimSpace(string(raw)), 200)).WithProvider(c.providerID)
	}
	return json.Unmarshal(raw, out)
}
