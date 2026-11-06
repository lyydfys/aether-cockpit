package reverse

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

// SupportsModelList 说明该渠道是否有可用的上游模型列表端点。
func (p *Provider) SupportsModelList(env *provider.AccountEnv) bool {
	return p.modelListPath() != ""
}

func (p *Provider) modelListPath() string {
	sp := spec.Load(p.profile.ID)
	return strings.TrimSpace(sp.Endpoint("models", p.profile.ModelListPath))
}

// ListModels 实现 provider.ModelLister：读取上游 /models 一类端点。
func (p *Provider) ListModels(ctx context.Context, env *provider.AccountEnv) ([]string, error) {
	path := p.modelListPath()
	if path == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUnsupported,
			"渠道 "+p.profile.ID+" 没有上游模型列表端点").WithProvider(p.profile.ID)
	}
	sp := spec.Load(p.profile.ID)
	baseURL := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if baseURL == "" && sp != nil {
		baseURL = strings.TrimRight(strings.TrimSpace(sp.BaseURL), "/")
	}
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(p.profile.DefaultBaseURL), "/")
	}
	if baseURL == "" {
		return nil, p.notConfigured("base_url")
	}
	endpoint := baseURL + path
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		endpoint = path
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造模型列表请求失败").WithCause(err)
	}
	req.Header.Set("Accept", "application/json")
	if err := applyAuth(req, env, modelListAuth(sp, &p.profile)); err != nil {
		return nil, err.WithProvider(p.profile.ID)
	}
	if sp != nil && sp.ClientVersion != "" {
		req.Header.Set("User-Agent", "openai-node/"+sp.ClientVersion)
	}
	if len(p.profile.Headers) > 0 {
		applyHeaders(req, env, p.profile.Headers)
	}
	if sp != nil {
		applyHeaders(req, env, sp.HeadersTemplate)
	}

	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "模型列表连接失败").
			WithCause(err).WithProvider(p.profile.ID)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, mapStatusCode(resp.StatusCode, truncate(string(raw), 300), resp.Header).WithProvider(p.profile.ID)
	}
	ids, err := provider.ParseModelListIDs(raw)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "模型列表无法解析: "+err.Error()).
			WithProvider(p.profile.ID)
	}
	return ids, nil
}
