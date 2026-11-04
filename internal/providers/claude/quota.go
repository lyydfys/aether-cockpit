package claude

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// claudeUsagePath 是 Claude Code 自己 /usage 命令背后的订阅用量端点。
// 它只认 OAuth token（Bearer），不接受 API key；返回 five_hour / seven_day
// 等窗口的 utilization（0-100）与 resets_at。端点写在账号 base_url 下，
// 自建中转若没有该路由会 404，届时由调用方降级而不是判账号失效。
const claudeUsagePath = "/api/oauth/usage"

// quotaHTTPTimeout 给用量读取一个短上限：它只用于展示，
// 不能像对话请求那样占用几十秒。
const quotaHTTPTimeout = 12 * time.Second

// windows 的展示名。上游 key 与 UI 文案分开，避免把内部字段名直接暴露给用户。
var usageWindowNames = []struct {
	key  string
	name string
}{
	{"five_hour", "5 hours"},
	{"seven_day", "Weekly"},
	{"seven_day_opus", "Weekly · Opus"},
	{"seven_day_sonnet", "Weekly · Sonnet"},
	// 新版还有 fable 档；不认识的窗口一律忽略，不影响其余窗口展示。
	{"seven_day_fable", "Weekly · Fable"},
}

type usageWindowJSON struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    any      `json:"resets_at"`
}

// limitsJSON 是新版响应里的模型专属限额（Pro/Max 的"某个模型单独算周额度"）。
//
// 它与顶层 seven_day_* 是**两套口径**：顶层是账号总周额度，limits 里
// weekly_scoped 是按模型分开的。两套都展示，用户才不会误以为某个模型
// 还能用（实际它自己的周额度已经见底）。
type limitsJSON struct {
	Kind     string   `json:"kind"`  // session / weekly / weekly_scoped
	Group    string   `json:"group"` // weekly
	Percent  *float64 `json:"percent"`
	ResetsAt any      `json:"resets_at"`
	Scope    struct {
		Model struct {
			DisplayName string `json:"display_name"`
			ID          string `json:"id"`
		} `json:"model"`
	} `json:"scope"`
}

// extraUsageJSON 是"超出套餐后用余额继续"的计量，老响应里没有。
type extraUsageJSON struct {
	MonthlyLimit *float64 `json:"monthly_limit"`
	UsedCredits  *float64 `json:"used_credits"`
	Currency     string   `json:"currency"`
}

// QueryQuota 读取 Claude 订阅的 5 小时 / 每周窗口。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token, err := p.ensureAccessToken(ctx, env)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, quotaHTTPTimeout)
	defer cancel()

	endpoint := baseURL(env) + claudeUsagePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Claude 用量请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("anthropic-beta", oauthBetaHeader)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", resolveUserAgent(env))

	client := env.Client()
	resp, err := client.Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 用量接口连接失败").WithCause(err).WithProvider(channelID)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Claude 用量接口拒绝凭证").WithProvider(channelID)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Claude 用量接口返回 "+resp.Status).WithProvider(channelID)
	}

	var payload struct {
		Windows map[string]usageWindowJSON `json:"-"`
		Limits  []limitsJSON               `json:"limits"`
		Extra   *extraUsageJSON            `json:"extra_usage"`
	}
	// 顶层是动态 key（five_hour / seven_day / …），所以两遍解析：
	// 先按 map 取窗口，再按固定结构取 limits 与 extra_usage。
	var flat map[string]json.RawMessage
	if err := json.Unmarshal(body, &flat); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 用量响应解析失败").WithCause(err)
	}
	payload.Windows = make(map[string]usageWindowJSON, len(flat))
	for _, w := range usageWindowNames {
		if raw, ok := flat[w.key]; ok {
			var one usageWindowJSON
			if err := json.Unmarshal(raw, &one); err == nil {
				payload.Windows[w.key] = one
			}
		}
	}
	if raw, ok := flat["limits"]; ok {
		_ = json.Unmarshal(raw, &payload.Limits)
	}
	if raw, ok := flat["extra_usage"]; ok {
		var extra extraUsageJSON
		if err := json.Unmarshal(raw, &extra); err == nil {
			payload.Extra = &extra
		}
	}

	q := &provider.QuotaStatus{ReadAt: time.Now().Unix()}
	for _, w := range usageWindowNames {
		win, ok := payload.Windows[w.key]
		if !ok || win.Utilization == nil {
			continue
		}
		used := *win.Utilization
		if used < 0 {
			used = 0
		}
		if used > 100 {
			used = 100
		}
		q.Windows = append(q.Windows, provider.QuotaWindow{
			Name:     w.name,
			Used:     used,
			ResetsAt: usageReset(used, win.ResetsAt),
		})
	}
	// 模型专属周额度：只展示 weekly_scoped，且必须带模型名（否则会和
	// 顶层周额度重复成两条同名窗口）。
	for _, lim := range payload.Limits {
		if lim.Kind != "weekly_scoped" || lim.Percent == nil {
			continue
		}
		name := strings.TrimSpace(lim.Scope.Model.DisplayName)
		if name == "" {
			name = strings.TrimSpace(lim.Scope.Model.ID)
		}
		if name == "" {
			continue
		}
		used := *lim.Percent
		if used < 0 {
			used = 0
		}
		if used > 100 {
			used = 100
		}
		q.Windows = append(q.Windows, provider.QuotaWindow{
			Name:     "Weekly · " + name,
			Used:     used,
			ResetsAt: usageReset(used, lim.ResetsAt),
		})
	}
	if payload.Extra != nil && payload.Extra.MonthlyLimit != nil && *payload.Extra.MonthlyLimit > 0 {
		used := 0.0
		if payload.Extra.UsedCredits != nil {
			used = *payload.Extra.UsedCredits
		}
		pct := used / *payload.Extra.MonthlyLimit * 100
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}
		currency := strings.ToUpper(strings.TrimSpace(payload.Extra.Currency))
		if currency == "" {
			currency = "USD"
		}
		q.Windows = append(q.Windows, provider.QuotaWindow{
			Name: "套餐外用量（" + currency + "）",
			Used: pct,
		})
	}
	if len(q.Windows) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 用量响应里没有窗口数据").WithProvider(channelID)
	}
	return q, nil
}

// usageReset 只在"确实用了额度"时才认 resets_at。
//
// 上游在用量为 0 时也会给一个占位时间（有时是过去的时间戳），直接展示
// 会出现"0% 却显示 2 小时后重置"这种自相矛盾的信息。
func usageReset(used float64, raw any) int64 {
	if used <= 0 {
		return 0
	}
	return parseUsageReset(raw)
}

// parseUsageReset 接受上游两种写法：RFC3339 字符串与 Unix 秒。
func parseUsageReset(v any) int64 {
	switch x := v.(type) {
	case string:
		if x = strings.TrimSpace(x); x == "" {
			return 0
		}
		if t, err := time.Parse(time.RFC3339, x); err == nil {
			return t.Unix()
		}
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t.Unix()
		}
	case float64:
		if x > 1e12 {
			return int64(x / 1000)
		}
		if x > 0 {
			return int64(x)
		}
	}
	return 0
}

var _ provider.QuotaCapability = (*Provider)(nil)
