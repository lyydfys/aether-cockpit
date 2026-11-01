package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/provider"
)

// xAI Grok CLI 的公开 OAuth 客户端参数（见 auth.x.ai OIDC discovery）。
const (
	xaiDeviceCodeURL = "https://auth.x.ai/oauth2/device/code"
	xaiTokenURL      = "https://auth.x.ai/oauth2/token"
	xaiClientID      = "b1a00492-073a-47ea-816f-4c329264a828"
)

var xaiScopes = []string{"openid", "profile", "email", "offline_access", "grok-cli:access", "api:access"}

// xaiProvider 在通用 OpenAI 兼容实现上补 xAI 的设备码刷新。
type xaiProvider struct{ Provider }

// RefreshCredential 用 refresh_token 换新 access_token；xAI 的 token 端点
// 是标准 OAuth2 form，不需要额外的设备指纹。
func (p *xaiProvider) RefreshCredential(ctx context.Context, env *provider.AccountEnv) (*provider.CredentialDraft, error) {
	refresh := strings.TrimSpace(env.GetCredential("refresh_token"))
	if refresh == "" {
		if access := strings.TrimSpace(env.GetCredential("access_token")); access != "" {
			return &provider.CredentialDraft{Label: env.Label, Credentials: map[string]string{
				"access_token": access, "expires_at": env.GetCredential("expires_at"),
			}}, nil
		}
		return nil, errors.New("xAI 账号缺少 refresh_token / access_token")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {xaiClientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, xaiTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := env.Client()
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("xAI 刷新失败 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return nil, fmt.Errorf("xAI 刷新响应无效: %s", strings.TrimSpace(string(body)))
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = refresh
	}
	expires := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	if tok.ExpiresIn <= 0 {
		expires = time.Now().Add(time.Hour)
	}
	return &provider.CredentialDraft{
		Label:     env.Label,
		ExpiresAt: expires.Unix(),
		Credentials: map[string]string{
			"access_token":  tok.AccessToken,
			"refresh_token": tok.RefreshToken,
			"expires_at":    expires.UTC().Format(time.RFC3339),
		},
	}, nil
}
