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
}

type usageWindowJSON struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    any      `json:"resets_at"`
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

	var payload map[string]usageWindowJSON
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 用量响应解析失败").WithCause(err)
	}

	q := &provider.QuotaStatus{ReadAt: time.Now().Unix()}
	for _, w := range usageWindowNames {
		win, ok := payload[w.key]
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
			ResetsAt: parseUsageReset(win.ResetsAt),
		})
	}
	if len(q.Windows) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 用量响应里没有窗口数据").WithProvider(channelID)
	}
	return q, nil
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
