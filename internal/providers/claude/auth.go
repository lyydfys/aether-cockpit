package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// OAuth 相关端点与常量。
const (
	// defaultAPIBaseURL 是订阅对话的官方端点。
	defaultAPIBaseURL = "https://api.anthropic.com"
	// defaultTokenURL 是订阅 OAuth 的刷新端点。与对话端点不同域：
	// 对话在 api.anthropic.com，令牌管理在 console.anthropic.com。
	defaultTokenURL = "https://console.anthropic.com/v1/oauth/token"

	// anthropicVersion 是 Messages API 的版本头，官方 SDK 固定值。
	anthropicVersion = "2023-06-01"

	// oauthBetaHeader 是订阅 token 必须携带的 beta 标记。
	// 来源：Claude Code 订阅流量抓包与社区代理实现（LiteLLM / promptfoo /
	// agentgateway 文档）均确认：OAuth token 只有带上
	// `anthropic-beta: oauth-2025-04-20` 才能调用 /v1/messages，
	// 缺失时上游返回 401。可用凭证里的 anthropic_beta 字段覆盖。
	// 它是 chat.go 里 defaultBetaHeader 的组成部分之一。
	oauthBetaHeader = "oauth-2025-04-20"

	// defaultClientID 是 Claude Code CLI 的公开 OAuth 客户端 ID。
	// 来源：Claude Code OAuth 流程逆向笔记（cedws gist）与多个开源代理
	// 实现一致引用该值；它属于公开信息（类似 antigravity 的 client_id）。
	// .credentials.json 通常不携带 client_id，因此刷新时用它兜底；
	// 用户可通过凭证字段 oauth_client_id 覆盖（自建中转换了客户端时）。
	defaultClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

	// tokenSafetyWindow 提前刷新窗口：避免卡着过期点发请求，
	// 流式请求中途 401 会直接断流，客户端体验很差。
	tokenSafetyWindow = 5 * time.Minute
)

// ensureAccessToken 返回一个当前可用的 access token。
//
// 策略与 antigravity 一致：过期窗口内主动刷新，而不是等 401 后补救。
func (p *Provider) ensureAccessToken(ctx context.Context, env *provider.AccountEnv) (string, error) {
	token := strings.TrimSpace(env.GetCredential("access_token"))
	expiresAt := parseExpiry(env.GetCredential("expires_at"))
	needRefresh := token == "" || (!expiresAt.IsZero() && time.Now().After(expiresAt.Add(-tokenSafetyWindow)))

	if !needRefresh {
		return token, nil
	}
	refresh := strings.TrimSpace(env.GetCredential("refresh_token"))
	if refresh == "" {
		// 没有 refresh_token 就只能用手头的 token（可能是长时效手动粘贴的）。
		if token != "" {
			return token, nil
		}
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Claude 订阅账号缺少 access_token，且没有 refresh_token；请重新导入本机登录态或手动粘贴凭证").
			WithProvider(channelID)
	}
	if err := p.refreshAccessToken(ctx, env, refresh); err != nil {
		return "", err
	}
	return env.GetCredential("access_token"), nil
}

// refreshAccessToken 用 refresh_token 换新 access_token，并把结果写回账号。
//
// 请求体为 JSON（Claude Code 官方客户端即此形态；console 端点同时接受
// form 编码，这里只实现 JSON 并在单测里固定契约）。
func (p *Provider) refreshAccessToken(ctx context.Context, env *provider.AccountEnv, refreshToken string) error {
	payload, err := json.Marshal(map[string]any{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     resolveClientID(env),
	})
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeInternal, "构造 Claude 刷新请求失败").WithCause(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL(env), strings.NewReader(string(payload)))
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeInternal, "构造 Claude 刷新请求失败").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := env.Client()
	resp, err := client.Do(req)
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 刷新令牌请求失败").
			WithCause(err).WithProvider(channelID).WithRetryable(true)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 刷新失败可能是 refresh_token 被吊销 / 已轮换：明确报错，
		// 让上层（gateway.RefreshAccount）保留原凭证并标记状态。
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth,
			"Claude 刷新令牌失败（%d）：%s", resp.StatusCode, truncate(string(body), 300)).
			WithProvider(channelID)
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"` // 秒；订阅 token 常见 8 小时
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 刷新响应无法解析").WithCause(err)
	}
	if strings.TrimSpace(tr.AccessToken) == "" {
		return ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Claude 刷新响应缺少 access_token").WithProvider(channelID)
	}

	// 先写回 env 再返回 draft：这样即使上层忽略 draft，账号也已经是新凭证。
	env.SetCredential("access_token", tr.AccessToken)
	if strings.TrimSpace(tr.RefreshToken) != "" {
		// Anthropic 的 refresh token 采用轮换策略：响应里给了新值就必须替换，
		// 旧值随即作废，否则下一次刷新会撞 invalid_grant。
		env.SetCredential("refresh_token", tr.RefreshToken)
	}
	if tr.ExpiresIn > 0 {
		env.SetCredential("expires_at", time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second).Format(time.RFC3339))
	}
	return nil
}

// RefreshCredential 实现 TokenRefreshCapability，供网关定时保活与手动刷新调用。
//
// 网关契约（gateway.RefreshAccount）：成功时返回的 CredentialDraft 会被
// applyCredential 整体写回账号；失败返回 error，上层恢复原状态。
func (p *Provider) RefreshCredential(ctx context.Context, env *provider.AccountEnv) (*provider.CredentialDraft, error) {
	refresh := strings.TrimSpace(env.GetCredential("refresh_token"))
	if refresh == "" {
		// 手动粘贴的长期 token 没有 refresh_token：没有可刷新的东西，
		// 返回当前状态即可（与 antigravity 的宽容策略一致）。
		if token := strings.TrimSpace(env.GetCredential("access_token")); token != "" {
			draft := &provider.CredentialDraft{
				Label: "Claude 订阅",
				Credentials: map[string]string{
					"access_token":    token,
					"refresh_token":   "",
					"expires_at":      env.GetCredential("expires_at"),
					"oauth_client_id": env.GetCredential("oauth_client_id"),
				},
			}
			draft.ExpiresAt = parseExpiry(draft.Credentials["expires_at"]).Unix()
			if draft.ExpiresAt < 0 {
				draft.ExpiresAt = 0
			}
			return draft, nil
		}
		// 只有 session_key（无 access/refresh）：走三步流自动换取 OAuth
		// 凭证（sessionkey.go），换取成功后按正常刷新路径返回 draft。
		if strings.TrimSpace(env.GetCredential("session_key")) != "" {
			if err := p.exchangeSessionKeyIntoEnv(ctx, env); err != nil {
				return nil, err
			}
			draft := &provider.CredentialDraft{
				Label: "Claude 订阅",
				Credentials: map[string]string{
					"access_token":    env.GetCredential("access_token"),
					"refresh_token":   env.GetCredential("refresh_token"),
					"expires_at":      env.GetCredential("expires_at"),
					"oauth_client_id": env.GetCredential("oauth_client_id"),
				},
			}
			draft.ExpiresAt = parseExpiry(draft.Credentials["expires_at"]).Unix()
			if draft.ExpiresAt < 0 {
				draft.ExpiresAt = 0
			}
			return draft, nil
		}
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Claude 订阅账号既没有 refresh_token 也没有 access_token；请重新导入本机登录态").
			WithProvider(channelID)
	}

	if err := p.refreshAccessToken(ctx, env, refresh); err != nil {
		return nil, err
	}

	draft := &provider.CredentialDraft{
		Label: "Claude 订阅",
		Credentials: map[string]string{
			"access_token":    env.GetCredential("access_token"),
			"refresh_token":   env.GetCredential("refresh_token"),
			"expires_at":      env.GetCredential("expires_at"),
			"oauth_client_id": env.GetCredential("oauth_client_id"),
		},
	}
	draft.ExpiresAt = parseExpiry(draft.Credentials["expires_at"]).Unix()
	if draft.ExpiresAt < 0 {
		draft.ExpiresAt = 0
	}
	return draft, nil
}

// tokenURL 返回刷新端点；凭证里显式填 oauth_token_url 时优先（自建中转 / 测试）。
func tokenURL(env *provider.AccountEnv) string {
	if v := strings.TrimSpace(env.GetCredential("oauth_token_url")); v != "" {
		return v
	}
	return defaultTokenURL
}

// resolveClientID 决定刷新请求用的 client_id：
// 凭证里的 oauth_client_id（自建中转换了客户端）> Claude Code 官方公开 ID。
func resolveClientID(env *provider.AccountEnv) string {
	if v := strings.TrimSpace(env.GetCredential("oauth_client_id")); v != "" {
		return v
	}
	return defaultClientID
}

// parseExpiry 解析 expires_at 字段。兼容三种形态：
// RFC3339 字符串（本包导入时写回的格式）、Unix 秒、Unix 毫秒
// （Claude Code 原生 credentials.json 里 expiresAt 是毫秒数）。
func parseExpiry(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	var n int64
	if _, err := fmt.Sscanf(raw, "%d", &n); err == nil && n > 0 {
		if n > 1e12 { // 毫秒级时间戳
			n /= 1000
		}
		return time.Unix(n, 0)
	}
	return time.Time{}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
