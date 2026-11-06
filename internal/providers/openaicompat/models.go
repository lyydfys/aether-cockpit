package openaicompat

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// SupportsModelList 说明该 OpenAI 兼容渠道是否有可用的 /models 端点。
// Azure 的部署路径与 Bedrock 的签名端点不是标准 /models，交给静态目录。
func (p *Provider) SupportsModelList(env *provider.AccountEnv) bool {
	return !p.cfg.Azure && p.cfg.SigV4Service == ""
}

// ListModels 实现 provider.ModelLister。
func (p *Provider) ListModels(ctx context.Context, env *provider.AccountEnv) ([]string, error) {
	if !p.SupportsModelList(env) {
		return nil, ir.NewAPIError(ir.ErrCodeUnsupported,
			"渠道 "+p.cfg.ID+" 没有标准的模型列表端点").WithProvider(p.cfg.ID)
	}
	sp := spec.Load(p.cfg.ID)
	if sp == nil && p.cfg.ID != "" && p.cfg.ID != "openai-compat" {
		sp = spec.Load("openai-compat")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if baseURL == "" && sp != nil {
		baseURL = strings.TrimRight(strings.TrimSpace(sp.BaseURL), "/")
	}
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(p.cfg.BaseURL), "/")
	}
	if baseURL == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "账号缺少 base_url").WithProvider(p.cfg.ID)
	}
	apiKey := strings.TrimSpace(env.GetCredential("api_key"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(env.GetCredential("access_token"))
	}
	if apiKey == "" && !p.cfg.NoKey {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "账号缺少 api_key").WithProvider(p.cfg.ID)
	}
	endpoint := baseURL + sp.Endpoint("models", "/models")

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造模型列表请求失败").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		if p.cfg.AuthStyle == "api-key" {
			req.Header.Set("api-key", apiKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	if sp != nil && sp.ClientVersion != "" {
		req.Header.Set("User-Agent", "openai-node/"+sp.ClientVersion)
	}
	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "模型列表连接失败").
			WithCause(err).WithProvider(p.cfg.ID)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := string(raw)
		if rs := []rune(msg); len(rs) > 300 {
			msg = string(rs[:300]) + "…"
		}
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"模型列表返回 %d：%s", resp.StatusCode, msg).WithProvider(p.cfg.ID)
	}
	ids, err := provider.ParseModelListIDs(raw)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "模型列表无法解析: "+err.Error()).
			WithProvider(p.cfg.ID)
	}
	return ids, nil
}
