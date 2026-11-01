package codebuddy

// CodeBuddy token 刷新：POST {base}/v2/plugin/auth/token/refresh，
// 当前 access token 走 Bearer，refresh token 走 X-Refresh-Token。
// 依据 Coding2API src/provider/codebuddy/refresh.py。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const cbRefreshPath = "/v2/plugin/auth/token/refresh"

// RefreshCredential 刷新 access token；上游轮换 refresh token 时一并更新。
func (p *Provider) RefreshCredential(ctx context.Context, env *provider.AccountEnv) (*provider.CredentialDraft, error) {
	token := strings.TrimSpace(cbFirst(env.GetCredential("token"), env.GetCredential("access_token")))
	refresh := strings.TrimSpace(env.GetCredential("refresh_token"))
	if token == "" || refresh == "" {
		env.MarkInvalid("缺少 token 或 refresh_token，需要重新登录")
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "CodeBuddy 缺少 token/refresh_token，需要重新登录").
			WithProvider(p.profile.id)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = p.profile.baseURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+cbRefreshPath, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 CodeBuddy 刷新请求失败").WithCause(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Refresh-Token", refresh)
	req.Header.Set("X-Auth-Refresh-Source", "plugin")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	if u, err := url.Parse(base); err == nil && u.Host != "" {
		req.Header.Set("X-Domain", u.Host)
	}

	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "CodeBuddy 刷新连接失败").
			WithCause(err).WithProvider(p.profile.id).WithRetryable(true)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		env.MarkInvalid("refresh token 已失效，需要重新登录")
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "CodeBuddy refresh token 已失效，需要重新登录").
			WithProvider(p.profile.id)
	}
	if resp.StatusCode >= 400 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "CodeBuddy 刷新返回 %d：%s",
			resp.StatusCode, truncateText(strings.TrimSpace(string(raw)), 300)).WithProvider(p.profile.id)
	}

	var body struct {
		Code int `json:"code"`
		Data struct {
			AccessToken  string `json:"accessToken"`
			RefreshToken string `json:"refreshToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || body.Code != 0 || strings.TrimSpace(body.Data.AccessToken) == "" {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "CodeBuddy 刷新响应异常：%s",
			truncateText(strings.TrimSpace(string(raw)), 300)).WithProvider(p.profile.id)
	}

	creds := make(map[string]string, len(env.Credentials)+2)
	for k, v := range env.Credentials {
		creds[k] = v
	}
	creds["token"] = body.Data.AccessToken
	if rt := strings.TrimSpace(body.Data.RefreshToken); rt != "" {
		creds["refresh_token"] = rt
	}
	return &provider.CredentialDraft{Credentials: creds, Source: "codebuddy_refresh"}, nil
}
