// Package antigravity 是 Antigravity（Google Cloud Code Assist 变体）渠道。
//
// 协议要点（与官方 Antigravity 客户端一致）：
//   - OAuth：Google OAuth2，scope 含 cloud-platform / userinfo / cclog；
//   - 项目发现：POST /v1internal:loadCodeAssist，取 cloudaicompanionProject；
//   - 对话：POST /v1internal:streamGenerateContent?alt=sse，请求体为
//     {model, userAgent, requestType, project, requestId, request:{contents,...}}。
//
// 本包负责把 IR 请求翻译成 Gemini 风格请求，并把 SSE 里的
// candidates / usageMetadata 还原成 IR 的极简事件。
package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Antigravity 的公开客户端参数。这些值与官方客户端一致，属于公开信息。
const (
	clientID     = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	clientSecret = "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"

	tokenEndpoint = "https://oauth2.googleapis.com/token"
	dailyBaseURL  = "https://daily-cloudcode-pa.googleapis.com"

	// 官方客户端 UA。Cloud Code 对低于 2.23.0 的客户端会拒绝新模型。
	userAgent = "antigravity/hub/2.24.0 (aidev_client; os_type=darwin; arch=arm64; cl=1000335682)"

	tokenSafetyWindow = 5 * time.Minute
)

var oauthScopes = []string{
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/cclog",
	"https://www.googleapis.com/auth/experimentsandconfigs",
}

// ensureAccessToken 保证账号有可用的 access token。
//
// 过期窗口内主动刷新，而不是等 401 后再补救：Antigravity 的 401
// 会同时中断正在进行的流，客户端体验很差。
func (p *Provider) ensureAccessToken(ctx context.Context, env *provider.AccountEnv) (string, error) {
	token := strings.TrimSpace(env.GetCredential("access_token"))
	expiresAt := parseExpiry(env.GetCredential("expires_at"))
	needRefresh := token == "" || (!expiresAt.IsZero() && time.Now().After(expiresAt.Add(-tokenSafetyWindow)))

	if needRefresh {
		refresh := strings.TrimSpace(env.GetCredential("refresh_token"))
		if refresh == "" {
			if token != "" {
				return token, nil
			}
			return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth,
				"Antigravity 账号缺少 access_token，且没有 refresh_token；请重新导入本机登录态或粘贴刷新令牌").
				WithProvider("antigravity")
		}
		if err := p.refreshAccessToken(ctx, env, refresh); err != nil {
			return "", err
		}
		return env.GetCredential("access_token"), nil
	}
	return token, nil
}

// refreshAccessToken 使用 refresh_token 换新 access_token，并写回账号。
func (p *Provider) refreshAccessToken(ctx context.Context, env *provider.AccountEnv, refreshToken string) error {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeInternal, "构造 Antigravity 刷新请求失败").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Go-http-client/2.0")

	client := env.Client()
	resp, err := client.Do(req)
	if err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 刷新令牌请求失败"+refreshHint(err)).
			WithCause(err).WithProvider("antigravity").WithRetryable(true)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth,
			"Antigravity 刷新令牌失败（%d）：%s", resp.StatusCode, truncate(string(body), 300)).
			WithProvider("antigravity")
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 刷新响应无法解析").WithCause(err)
	}
	if strings.TrimSpace(tr.AccessToken) == "" {
		return ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Antigravity 刷新响应缺少 access_token").
			WithProvider("antigravity")
	}

	env.SetCredential("access_token", tr.AccessToken)
	if strings.TrimSpace(tr.RefreshToken) != "" {
		env.SetCredential("refresh_token", tr.RefreshToken)
	}
	if strings.TrimSpace(tr.IDToken) != "" {
		env.SetCredential("id_token", tr.IDToken)
	}
	if tr.ExpiresIn > 0 {
		env.SetCredential("expires_at", time.Now().Add(time.Duration(tr.ExpiresIn)*time.Second).
			Format(time.RFC3339))
	}
	return nil
}

// ensureProjectID 返回账号绑定的 GCP 项目；缺失时通过 loadCodeAssist 发现。
func (p *Provider) ensureProjectID(ctx context.Context, env *provider.AccountEnv, accessToken string) (string, error) {
	if id := strings.TrimSpace(env.GetCredential("project_id")); id != "" {
		return id, nil
	}
	if strings.TrimSpace(accessToken) == "" {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Antigravity 缺少 access_token").WithProvider("antigravity")
	}

	body := []byte(`{"metadata":{"ideType":"ANTIGRAVITY"}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL(env)+"/v1internal:loadCodeAssist", strings.NewReader(string(body)))
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeInternal, "构造 Antigravity 项目发现请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	client := env.Client()
	resp, err := client.Do(req)
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 项目发现请求失败").
			WithCause(err).WithProvider("antigravity").WithRetryable(true)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth,
			"Antigravity 项目发现失败（%d）：%s", resp.StatusCode, truncate(string(raw), 300)).
			WithProvider("antigravity")
	}

	var load struct {
		CloudaicompanionProject any `json:"cloudaicompanionProject"`
		ProjectID               any `json:"projectId"`
		Project                 any `json:"project"`
	}
	if err := json.Unmarshal(raw, &load); err != nil {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 项目发现响应无法解析").WithCause(err)
	}

	projectID := extractProject(load.CloudaicompanionProject, load.ProjectID, load.Project)
	if projectID == "" {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Antigravity 账号没有返回 cloudaicompanionProject；该账号可能尚未完成 onboarding").
			WithProvider("antigravity")
	}
	env.SetCredential("project_id", projectID)
	return projectID, nil
}

// extractProject 兼容字符串与 {"id": "..."} 两种返回形态。
func extractProject(values ...any) string {
	for _, value := range values {
		switch v := value.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case map[string]any:
			if s, ok := v["id"].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

// RefreshCredential 实现 TokenRefreshCapability，供内核定时保活调用。
func (p *Provider) RefreshCredential(ctx context.Context, env *provider.AccountEnv) (*provider.CredentialDraft, error) {
	refresh := strings.TrimSpace(env.GetCredential("refresh_token"))
	token, _ := p.ensureAccessToken(ctx, env)
	if refresh != "" {
		if err := p.refreshAccessToken(ctx, env, refresh); err != nil {
			return nil, err
		}
		token = env.GetCredential("access_token")
	}
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Antigravity 刷新后仍无 access_token").
			WithProvider("antigravity")
	}

	draft := &provider.CredentialDraft{
		Label: "Antigravity",
		Credentials: map[string]string{
			"access_token":  env.GetCredential("access_token"),
			"refresh_token": env.GetCredential("refresh_token"),
			"expires_at":    env.GetCredential("expires_at"),
			"project_id":    env.GetCredential("project_id"),
			"id_token":      env.GetCredential("id_token"),
			"token_type":    env.GetCredential("token_type"),
		},
	}
	if projectID, err := p.ensureProjectID(ctx, env, token); err == nil && projectID != "" {
		draft.Credentials["project_id"] = projectID
	}
	draft.ExpiresAt = parseExpiry(env.GetCredential("expires_at")).Unix()
	if draft.ExpiresAt < 0 {
		draft.ExpiresAt = 0
	}
	return draft, nil
}

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
	if n, err := parseInt64(raw); err == nil && n > 0 {
		if n > 1e12 {
			n /= 1000
		}
		return time.Unix(n, 0)
	}
	return time.Time{}
}

func parseInt64(raw string) (int64, error) {
	var n int64
	_, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &n)
	return n, err
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// refreshHint 给网络类失败一句可操作的提示：本机到 Google OAuth 的链路
// 常常需要代理，只回一个 EOF 用户无从下手。
func refreshHint(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	for _, sig := range []string{"eof", "timeout", "timed out", "connection reset", "no such host", "dial tcp", "tls handshake"} {
		if strings.Contains(msg, sig) {
			return "（需要能访问 oauth2.googleapis.com 的网络或全局代理；当前请求在建立连接时失败）"
		}
	}
	return ""
}
