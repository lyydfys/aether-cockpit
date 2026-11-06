package geminicompat

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
	"aether-cockpit/internal/providers/antigravity"
)

// Config 描述一个 Gemini 原生协议渠道实例：通用 gemini-compat（自定义供应商 /
// 远程实例）与官方 Google 渠道共用同一实现。
type Config struct {
	ID          string
	DisplayName string
	Family      string
	RegionTag   string
	BrandColor  string
	Description string
	BaseURL     string
	Models      []ir.ModelMeta
	// OAuthEnabled 支持 Gemini CLI OAuth 本机导入与 Code Assist 上游。
	OAuthEnabled bool
}

// Provider 是 Google Gemini 原生协议（generateContent）上游。
type Provider struct{ cfg Config }

var defaultGeminiConfig = Config{
	ID: "gemini-compat", DisplayName: "Gemini 兼容", Family: "gemini", RegionTag: "GLOBAL", BrandColor: "#4285F4",
	Description: "Google Gemini 原生协议上游（generateContent）。自定义供应商选 Gemini 协议，或把远程 Aether 的 /v1beta 接进来；模型用 gemini-compat/<模型名>。",
	BaseURL:     "https://generativelanguage.googleapis.com",
	Models: []ir.ModelMeta{
		{ID: "gemini-compat/gemini-2.5-pro", UpstreamModel: "gemini-2.5-pro", DisplayName: "Gemini 2.5 Pro", ContextWindow: 1048576,
			Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true}},
		{ID: "gemini-compat/gemini-2.5-flash", UpstreamModel: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash", ContextWindow: 1048576,
			Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true}},
		{ID: "gemini-compat/gemini-2.0-flash", UpstreamModel: "gemini-2.0-flash", DisplayName: "Gemini 2.0 Flash", ContextWindow: 1048576,
			Capabilities: ir.ModelCapabilities{ToolCall: true, Vision: true, Streaming: true}},
	},
}

var googleConfig = Config{
	ID: "google", DisplayName: "Google Gemini", Family: "google", RegionTag: "GLOBAL", BrandColor: "#4285F4",
	Description: "Gemini Developer API（Google AI Studio 的 API Key），或导入本机 Gemini CLI 的 OAuth 登录态走 Code Assist。模型用 google/<模型名>。",
	BaseURL:     "https://generativelanguage.googleapis.com", OAuthEnabled: true,
	Models: []ir.ModelMeta{
		{ID: "google/gemini-2.5-pro", UpstreamModel: "gemini-2.5-pro", DisplayName: "Gemini 2.5 Pro", ContextWindow: 1048576,
			Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true}},
		{ID: "google/gemini-2.5-flash", UpstreamModel: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash", ContextWindow: 1048576,
			Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true}},
		{ID: "google/gemini-2.0-flash", UpstreamModel: "gemini-2.0-flash", DisplayName: "Gemini 2.0 Flash", ContextWindow: 1048576,
			Capabilities: ir.ModelCapabilities{ToolCall: true, Vision: true, Streaming: true}},
	},
}

func init() {
	provider.Register(&Provider{cfg: defaultGeminiConfig})
	provider.Register(&Provider{cfg: googleConfig})
}

func (p *Provider) Manifest() provider.ProviderManifest {
	cfg := p.cfg
	auths := []provider.AuthMethod{provider.AuthManualInput}
	apiKeyField := provider.FormFieldSchema{
		Key: "api_key", Label: "API Key", Type: "password", Required: true,
		Help: "放在 x-goog-api-key 请求头",
	}
	schema := []provider.FormFieldSchema{
		{
			Key: "base_url", Label: "Base URL", Type: "text",
			Placeholder: cfg.BaseURL + "/v1beta",
			Help:        "留空用官方地址；…/v1beta 或根地址都可以",
		},
		apiKeyField,
	}
	if cfg.OAuthEnabled {
		auths = []provider.AuthMethod{provider.AuthLocalImport, provider.AuthManualInput}
		schema[1].Required = false
		schema[1].Label = "API Key（可选）"
		schema = append(schema,
			provider.FormFieldSchema{Key: "access_token", Label: "OAuth Access Token（可选）", Type: "password", Help: "本机 Gemini CLI 导入会自动填好；API Key 与 OAuth 二选一"},
			provider.FormFieldSchema{Key: "refresh_token", Label: "OAuth Refresh Token（可选）", Type: "password"},
			provider.FormFieldSchema{Key: "oauth_type", Label: "OAuth 模式", Type: "select", Options: []string{"code_assist", "ai_studio"},
				Help: "code_assist = Gemini CLI/Code Assist；ai_studio = generativelanguage（需自定义 OAuth 客户端与 token）"},
			provider.FormFieldSchema{Key: "oauth_client_id", Label: "自定义 OAuth Client ID（可选）", Type: "text", Help: "AI Studio OAuth 通常需要自己的 OAuth 客户端"},
			provider.FormFieldSchema{Key: "oauth_client_secret", Label: "自定义 OAuth Client Secret（可选）", Type: "password"},
			provider.FormFieldSchema{Key: "project_id", Label: "Code Assist Project ID（可选）", Type: "text", Help: "OAuth 模式无法自动发现项目时手填"},
		)
	}
	return provider.ProviderManifest{
		ID: cfg.ID, DisplayName: cfg.DisplayName, Family: cfg.Family, RegionTag: cfg.RegionTag, BrandColor: cfg.BrandColor,
		Description:    cfg.Description,
		NetworkPolicy:  provider.NetPolicyProxyFirst,
		SupportedAuths: auths,
		AuthFormSchema: schema,
		Models:         cfg.Models,
		Capabilities:   provider.CapabilitySet{Chat: true, LocalImport: cfg.OAuthEnabled},
	}
}

func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	if p.cfg.OAuthEnabled && isGeminiOAuthAccount(env) {
		return p.chatOAuth(ctx, env, req)
	}
	key := strings.TrimSpace(env.GetCredential("api_key"))
	if key == "" {
		key = strings.TrimSpace(env.GetCredential("access_token"))
	}
	if key == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Gemini 兼容上游缺少 API Key").
			WithProvider(p.cfg.ID)
	}
	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = strings.TrimPrefix(strings.TrimSpace(req.Model), p.cfg.ID+"/")
	}
	model = strings.TrimPrefix(model, "models/")
	if model == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Gemini 兼容上游缺少模型名").
			WithProvider(p.cfg.ID)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(p.cfg.BaseURL), "/")
	}
	base = strings.TrimSuffix(base, "/v1")
	if !strings.HasSuffix(base, "/v1beta") {
		base += "/v1beta"
	}
	endpoint := base + "/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
	payload, err := json.Marshal(antigravity.GeminiBody(req, model))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Gemini 兼容上游请求序列化失败").WithCause(err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Gemini 兼容上游请求失败").WithCause(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-goog-api-key", key)
	httpReq.Header.Set("User-Agent", "google-genai/1.0.0")

	resp, err := env.Client().Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Gemini 兼容上游连接失败").
			WithCause(err).WithProvider(p.cfg.ID).WithRetryable(true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, mapUpstreamError(resp.StatusCode, string(raw), resp.Header, p.cfg.ID)
	}

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		antigravity.ParseGeminiSSE(ctx, out, resp.Body)
	}()
	return out, nil
}

func mapUpstreamError(status int, body string, hdr http.Header, providerID string) *ir.APIError {
	msg := truncateText(strings.TrimSpace(body), 400)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Gemini 兼容上游鉴权失败（%d）：%s", status, msg).
			WithProvider(providerID)
	case status == http.StatusTooManyRequests:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "Gemini 兼容上游限流：%s", msg).
			WithProvider(providerID).WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, msg))
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "Gemini 兼容上游拒绝请求：%s", msg).
			WithProvider(providerID)
	case status >= 500:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Gemini 兼容上游 %d：%s", status, msg).
			WithProvider(providerID).WithRetryable(true).WithStatus(http.StatusBadGateway)
	default:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Gemini 兼容上游返回 %d：%s", status, msg).
			WithProvider(providerID)
	}
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var (
	_ provider.ChatCapability        = (*Provider)(nil)
	_ provider.LocalImportCapability = (*Provider)(nil)
)
