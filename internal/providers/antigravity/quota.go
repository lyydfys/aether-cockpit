package antigravity

import (
	"bytes"
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

// QueryQuota 读取 Antigravity（Cloud Code Assist）的剩余额度。
//
// 与 高并发网关 / oauth-bridge 相同的数据源，不需要本地语言服务器：
//   - POST /v1internal:retrieveUserQuotaSummary {project}
//     → 按模型池（Gemini / Claude & GPT…）给出 5 小时与每周窗口；
//   - POST /v1internal:retrieveUserQuota {project}
//     → 每个模型的 remainingFraction / resetTime。
//
// 两者拼成前端认识的形状：池窗口带 pool，模型窗口带 family 且 pool 指向同一池，
// 用量页会把它们画成「Gemini 5h / 7d」「Claude 5h / 7d」这样的池-meter。
const quotaHTTPTimeout = 12 * time.Second

type quotaBucket struct {
	RemainingAmount   string   `json:"remainingAmount"`
	RemainingFraction *float64 `json:"remainingFraction"`
	ResetTime         string   `json:"resetTime"`
	TokenType         string   `json:"tokenType"`
	ModelID           string   `json:"modelId"`
}

type quotaSummaryBucket struct {
	BucketID          string   `json:"bucketId"`
	DisplayName       string   `json:"displayName"`
	Description       string   `json:"description"`
	Window            string   `json:"window"`
	Remaining         string   `json:"remaining"`
	RemainingFraction *float64 `json:"remainingFraction"`
	RemainingAmount   string   `json:"remainingAmount"`
	Disabled          bool     `json:"disabled"`
	ResetTime         string   `json:"resetTime"`
}

type quotaSummaryGroup struct {
	DisplayName string               `json:"displayName"`
	Description string               `json:"description"`
	Buckets     []quotaSummaryBucket `json:"buckets"`
}

type quotaSummaryResponse struct {
	Groups      []quotaSummaryGroup  `json:"groups"`
	Buckets     []quotaSummaryBucket `json:"buckets"`
	Description string               `json:"description"`
	// PaidTier 是付费档的积分余额；免费档没有这一段。
	PaidTier *struct {
		AvailableCredits []struct {
			CreditType    string   `json:"creditType"`
			CreditAmount  *float64 `json:"creditAmount"`
			MinimumAmount *float64 `json:"minimumCreditAmountForUsage"`
		} `json:"availableCredits"`
	} `json:"paidTier"`
}

type quotaResponse struct {
	Buckets []quotaBucket `json:"buckets"`
}

// QueryQuota 实现 provider.QuotaCapability。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token, err := p.ensureAccessToken(ctx, env)
	if err != nil {
		return nil, err
	}
	projectID, err := p.ensureProjectID(ctx, env, token)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, quotaHTTPTimeout)
	defer cancel()

	q := &provider.QuotaStatus{ReadAt: time.Now().Unix()}
	var errs []string

	summary, sumErr := p.quotaSummary(ctx, env, token, projectID)
	if sumErr != nil {
		errs = append(errs, sumErr.Message)
	}
	models, modelErr := p.quotaModels(ctx, env, token, projectID)
	if modelErr != nil {
		errs = append(errs, modelErr.Message)
	}
	if sumErr != nil && modelErr != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Antigravity 额度接口都失败了："+strings.Join(errs, "；")).
			WithProvider("antigravity")
	}

	pools := make([]string, 0, len(summaryGroups(summary)))
	for _, g := range summaryGroups(summary) {
		pool := strings.TrimSpace(g.DisplayName)
		if pool == "" {
			pool = "Antigravity"
		}
		pools = append(pools, pool)
		for _, b := range g.Buckets {
			if b.Disabled {
				continue
			}
			frac, ok := bucketFraction(b)
			if !ok {
				continue
			}
			q.Windows = append(q.Windows, provider.QuotaWindow{
				Name:     windowName(b.BucketID, b.Window, b.DisplayName),
				Pool:     pool,
				Used:     usedPercent(frac),
				ResetsAt: parseResetTime(b.ResetTime),
			})
		}
	}
	for _, b := range summaryBuckets(summary) {
		if b.Disabled {
			continue
		}
		frac, ok := bucketFraction(b)
		if !ok {
			continue
		}
		q.Windows = append(q.Windows, provider.QuotaWindow{
			Name:     windowName(b.BucketID, b.Window, b.DisplayName),
			Used:     usedPercent(frac),
			ResetsAt: parseResetTime(b.ResetTime),
		})
	}
	// 付费档的积分余额：不在窗口里，单独作为可用额度展示。
	if summary != nil && summary.PaidTier != nil {
		var credits float64
		for _, c := range summary.PaidTier.AvailableCredits {
			if c.CreditAmount != nil && *c.CreditAmount > 0 {
				credits += *c.CreditAmount
			}
		}
		if credits > 0 {
			q.Balance = credits
			q.BalanceUnit = "credits"
		}
	}
	if summary != nil && strings.TrimSpace(summary.Description) != "" {
		q.Message = truncate(strings.TrimSpace(summary.Description), 200)
	}

	if models != nil {
		added := 0
		for _, b := range models.Buckets {
			if b.ModelID == "" {
				continue
			}
			frac, ok := modelBucketFraction(b)
			if !ok {
				continue
			}
			fam := modelFamily(b.ModelID)
			// Chat / Tab 是 IDE 内部模型（内联补全、内嵌聊天）的额度桶，
			// 不属于对话额度，也不在网关模型清单里，不进入额度展示。
			if fam == "Chat" || fam == "Tab" {
				continue
			}
			q.Windows = append(q.Windows, provider.QuotaWindow{
				Name:     b.ModelID,
				Family:   fam,
				Pool:     poolForFamily(pools, fam),
				Used:     usedPercent(frac),
				ResetsAt: parseResetTime(b.ResetTime),
			})
			added++
			if added >= 40 {
				break
			}
		}
	}

	if len(q.Windows) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Antigravity 额度响应里没有窗口数据").WithProvider("antigravity")
	}
	return q, nil
}

func (p *Provider) quotaSummary(ctx context.Context, env *provider.AccountEnv, token, projectID string) (*quotaSummaryResponse, *ir.APIError) {
	raw, status, err := p.postCodeAssist(ctx, env, token, "/v1internal:retrieveUserQuotaSummary", map[string]any{"project": projectID})
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"Antigravity 额度汇总接口返回 %d：%s", status, truncate(string(raw), 200)).WithProvider("antigravity")
	}
	var out quotaSummaryResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 额度汇总响应无法解析").WithCause(err)
	}
	return &out, nil
}

func (p *Provider) quotaModels(ctx context.Context, env *provider.AccountEnv, token, projectID string) (*quotaResponse, *ir.APIError) {
	raw, status, err := p.postCodeAssist(ctx, env, token, "/v1internal:retrieveUserQuota", map[string]any{"project": projectID})
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"Antigravity 模型额度接口返回 %d：%s", status, truncate(string(raw), 200)).WithProvider("antigravity")
	}
	var out quotaResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 模型额度响应无法解析").WithCause(err)
	}
	return &out, nil
}

func (p *Provider) postCodeAssist(ctx context.Context, env *provider.AccountEnv, token, method string, body any) ([]byte, int, *ir.APIError) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, 0, ir.NewAPIError(ir.ErrCodeInternal, "Antigravity 额度请求序列化失败").WithCause(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(env)+method, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, ir.NewAPIError(ir.ErrCodeInternal, "构造 Antigravity 额度请求失败").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", antigravityUserAgent())
	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, 0, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 额度接口连接失败").
			WithCause(err).WithProvider("antigravity")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return raw, resp.StatusCode, nil
}

func summaryGroups(s *quotaSummaryResponse) []quotaSummaryGroup {
	if s == nil {
		return nil
	}
	return s.Groups
}

func summaryBuckets(s *quotaSummaryResponse) []quotaSummaryBucket {
	if s == nil {
		return nil
	}
	return s.Buckets
}

// bucketFraction 取一个额度桶的"剩余比例"。
//
// 上游有时只给 remainingAmount（字符串）而不给 remainingFraction。
// 直接按"缺 fraction 就跳过"会把整个窗口丢掉，用户看到的是"没有额度"。
// remainingAmount ≤ 1 时按比例用；> 1 说明是绝对积分，那把比例留空但
// 至少要给出额度存在的信息——这里选择按比例夹到 0-1 处理，避免误判成 0%。
func bucketFraction(b quotaSummaryBucket) (float64, bool) {
	if b.RemainingFraction != nil {
		return *b.RemainingFraction, true
	}
	return amountFraction(b.RemainingAmount)
}

func modelBucketFraction(b quotaBucket) (float64, bool) {
	if b.RemainingFraction != nil {
		return *b.RemainingFraction, true
	}
	return amountFraction(b.RemainingAmount)
}

// amountFraction 把 remainingAmount 解析成 0-1 的比例。
func amountFraction(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	if v < 0 {
		v = 0
	}
	if v > 1 {
		// 绝对积分（不是比例）：无法换算成百分比，按"满额"处理，
		// 让窗口仍然可见，具体数值由 paidTier 的积分余额那一路展示。
		v = 1
	}
	return v, true
}

func usedPercent(remainingFraction float64) float64 {
	if remainingFraction < 0 {
		remainingFraction = 0
	}
	if remainingFraction > 1 {
		remainingFraction = 1
	}
	return (1 - remainingFraction) * 100
}

// windowName 统一成 UI 能读出跨度的名字（5 hours / 7 days）。
//
// 优先用**已知 bucketId 白名单**判定跨度，再回落到字符串猜测：
// bucketId 是稳定的机器标识（`3p-5h` / `claude:weekly` / `gemini-argon-*-5h`），
// 而 window/displayName 是给人看的文案，随时可能改措辞（"Weekly limit"
// 改成 "每周额度" 就猜不出来了）。
func windowName(bucketID, window, display string) string {
	if name := windowNameFromBucketID(bucketID); name != "" {
		return name
	}
	w := strings.ToLower(strings.TrimSpace(window + " " + strings.TrimSpace(display)))
	switch {
	case strings.Contains(w, "5h"), strings.Contains(w, "5 h"), strings.Contains(w, "5 hour"), strings.Contains(w, "five"), strings.Contains(w, "5_hour"):
		return "5 hours"
	case strings.Contains(w, "7d"), strings.Contains(w, "7 d"), strings.Contains(w, "7 day"), strings.Contains(w, "seven"), strings.Contains(w, "week"), strings.Contains(w, "7_day"):
		return "7 days"
	case strings.Contains(w, "1d"), strings.Contains(w, "day"), strings.Contains(w, "daily"), strings.Contains(w, "24h"):
		return "1 day"
	}
	if d := strings.TrimSpace(display); d != "" {
		return d
	}
	if wt := strings.TrimSpace(window); wt != "" {
		return wt
	}
	return "Quota"
}

// windowNameFromBucketID 用已知 bucketId 判定窗口跨度，未知返回空串。
//
// 白名单来自上游实际在用的标识（见审计文档 A2）：
//
//	3p-5h / claude:5h / gemini-5h / argon:5h / gemini-argon-*-5h  → 5 hours
//	3p-weekly / claude:weekly / gemini-weekly / argon:weekly / …-weekly → 7 days
func windowNameFromBucketID(bucketID string) string {
	id := strings.ToLower(strings.TrimSpace(bucketID))
	if id == "" {
		return ""
	}
	switch {
	case strings.HasSuffix(id, "5h"), strings.Contains(id, ":5h"):
		return "5 hours"
	case strings.HasSuffix(id, "weekly"), strings.Contains(id, ":weekly"):
		return "7 days"
	case strings.HasSuffix(id, "daily"), strings.Contains(id, ":daily"), strings.HasSuffix(id, "24h"):
		return "1 day"
	}
	return ""
}

// modelFamily 把模型名归到池的家族名（与 summary 的池名对齐）。
func modelFamily(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "claude"):
		return "Claude"
	case strings.Contains(m, "gemini"):
		return "Gemini"
	case strings.Contains(m, "gpt"), strings.Contains(m, "oss"):
		return "GPT"
	}
	if i := strings.IndexAny(model, "-_"); i > 0 {
		return strings.ToUpper(model[:1]) + model[1:i]
	}
	return model
}

// poolForFamily 找名字里含该家族的池；找不到就空（模型窗口自己成家族）。
func poolForFamily(pools []string, family string) string {
	lf := strings.ToLower(family)
	for _, p := range pools {
		if strings.Contains(strings.ToLower(p), lf) {
			return p
		}
	}
	return ""
}

// parseResetTime 接受 RFC3339 / RFC3339Nano / Unix（秒或毫秒）字符串。
func parseResetTime(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.Unix()
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Unix()
	}
	if n, err := parseInt64(raw); err == nil && n > 0 {
		if n > 1e12 {
			n /= 1000
		}
		return n
	}
	return 0
}
