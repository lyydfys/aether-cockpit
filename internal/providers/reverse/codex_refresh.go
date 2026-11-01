package reverse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Codex ChatGPT 订阅的 token 刷新（Codex CLI 的公开 OAuth 客户端）。
// 只有带 refresh_token 的订阅账号才走这里；中转账号没有它。
var codexTokenURL = "https://auth.openai.com/oauth/token"

const codexClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

// RefreshCredential 实现 TokenRefreshCapability（仅 codex）。
func (p *Provider) RefreshCredential(ctx context.Context, env *provider.AccountEnv) (*provider.CredentialDraft, error) {
	if p.profile.ID != "codex" {
		return nil, ir.NewAPIError(ir.ErrCodeUnsupported,
			"渠道 "+p.profile.ID+" 没有订阅刷新").WithProvider(p.profile.ID)
	}
	rt := strings.TrimSpace(env.GetCredential("refresh_token"))
	if rt == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Codex 账号没有 refresh_token，需要重新登录").WithProvider(p.profile.ID)
	}

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {codexClientID},
		"refresh_token": {rt},
		"scope":         {"openid profile email"},
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Codex 刷新请求失败").WithCause(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "AetherCockpit/token-refresh")

	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Codex 刷新令牌请求失败（需要能访问 auth.openai.com 的网络）："+err.Error()).WithProvider(p.profile.ID)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth,
			"Codex 刷新被拒绝（%d）：%s", resp.StatusCode, msg).WithProvider(p.profile.ID)
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil || strings.TrimSpace(tok.AccessToken) == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Codex 刷新回复里没有 access_token").WithProvider(p.profile.ID)
	}
	creds := map[string]string{"access_token": tok.AccessToken}
	if tok.RefreshToken != "" {
		creds["refresh_token"] = tok.RefreshToken
	}
	if tok.IDToken != "" {
		creds["id_token"] = tok.IDToken
		if id := chatGPTAccountID(tok.IDToken); id != "" {
			creds["account_id"] = id
		}
	}
	return &provider.CredentialDraft{Credentials: creds}, nil
}

// chatGPTAccountID 从 Codex 的 id_token 里取 chatgpt_account_id。
// 不校验签名：令牌刚由官方端点返回，这里只用于取一个标识。
func chatGPTAccountID(idToken string) string {
	claims := idTokenClaims(idToken)
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if id, ok := auth["chatgpt_account_id"].(string); ok {
			return strings.TrimSpace(id)
		}
	}
	if id, ok := claims["chatgpt_account_id"].(string); ok {
		return strings.TrimSpace(id)
	}
	return ""
}

// idTokenClaims 解码 JWT 载荷（不校验签名），取不到返回空 map。
func idTokenClaims(idToken string) map[string]any {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

// codexAuthJSONDraft 从 Codex CLI 的 ~/.codex/auth.json 读取 ChatGPT 登录态。
// 这是订阅账号导入；config.toml 的自定义 provider 仍由内置规则处理。
func (p *Provider) codexAuthJSONDraft() *provider.CredentialDraft {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	path := filepath.Join(home, ".codex", "auth.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(raw, &f) != nil || strings.TrimSpace(f.Tokens.AccessToken) == "" {
		return nil
	}
	accountID := strings.TrimSpace(f.Tokens.AccountID)
	if accountID == "" {
		accountID = chatGPTAccountID(f.Tokens.IDToken)
	}
	creds := map[string]string{
		"access_token": f.Tokens.AccessToken,
		"base_url":     "https://chatgpt.com/backend-api/codex",
		"model":        "gpt-5-codex",
	}
	if f.Tokens.RefreshToken != "" {
		creds["refresh_token"] = f.Tokens.RefreshToken
	}
	if f.Tokens.IDToken != "" {
		creds["id_token"] = f.Tokens.IDToken
	}
	if accountID != "" {
		creds["account_id"] = accountID
	}
	label := "Codex ChatGPT"
	if email, _ := idTokenClaims(f.Tokens.IDToken)["email"].(string); strings.TrimSpace(email) != "" {
		label = "Codex (" + strings.TrimSpace(email) + ")"
	}
	return &provider.CredentialDraft{
		Label: label, Credentials: creds,
		Source: "local_import:" + path,
	}
}
