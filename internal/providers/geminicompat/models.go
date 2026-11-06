package geminicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// SupportsModelList 表示这个账号能否用 Gemini 的 /v1beta/models 列模型。
// Gemini CLI 的 OAuth 登录态走 Code Assist，模型目录不同，不走这里。
func (p *Provider) SupportsModelList(env *provider.AccountEnv) bool {
	if p.cfg.OAuthEnabled && isGeminiOAuthAccount(env) {
		return false
	}
	return strings.TrimSpace(env.GetCredential("api_key")) != ""
}

// ListModels 实现 provider.ModelLister：GET <base>/v1beta/models。
func (p *Provider) ListModels(ctx context.Context, env *provider.AccountEnv) ([]string, error) {
	key := strings.TrimSpace(env.GetCredential("api_key"))
	if key == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUnsupported,
			"Gemini OAuth 登录态没有公开的模型列表端点").WithProvider(p.cfg.ID)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(p.cfg.BaseURL), "/")
	}
	base = strings.TrimSuffix(base, "/v1")
	if !strings.HasSuffix(base, "/v1beta") {
		base += "/v1beta"
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ids := make([]string, 0, 64)
	seen := make(map[string]bool, 64)
	pageToken := ""
	for page := 0; page < 5; page++ {
		u, err := url.Parse(base + "/models")
		if err != nil {
			return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造模型列表地址失败").WithCause(err)
		}
		q := u.Query()
		q.Set("pageSize", "1000")
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造模型列表请求失败").WithCause(err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("x-goog-api-key", key)
		req.Header.Set("User-Agent", "google-genai/1.0.0")
		resp, err := env.Client().Do(req)
		if err != nil {
			return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "模型列表连接失败").
				WithCause(err).WithProvider(p.cfg.ID)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			msg := string(raw)
			if rs := []rune(msg); len(rs) > 300 {
				msg = string(rs[:300]) + "…"
			}
			return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
				"模型列表返回 %d：%s", resp.StatusCode, msg).WithProvider(p.cfg.ID)
		}
		var page struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
				"模型列表无法解析: "+err.Error()).WithProvider(p.cfg.ID)
		}
		for _, m := range page.Models {
			id := strings.TrimPrefix(strings.TrimSpace(m.Name), "models/")
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
		pageToken = strings.TrimSpace(page.NextPageToken)
		if pageToken == "" {
			break
		}
	}
	if len(ids) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Gemini 模型列表为空").WithProvider(p.cfg.ID)
	}
	return ids, nil
}
