package antigravity

import (
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func init() {
	provider.Register(&Provider{})
}

// Provider 实现 Antigravity 渠道。
type Provider struct{}

// manifestModels 是官方 Antigravity 当前可选的模型清单。
var manifestModels = []ir.ModelMeta{
	{
		ID: "antigravity/claude-opus-4-6-thinking", UpstreamModel: "claude-opus-4-6-thinking",
		DisplayName: "Claude Opus 4.6 (Thinking)", ContextWindow: 200000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true},
	},
	{
		ID: "antigravity/claude-sonnet-4-6", UpstreamModel: "claude-sonnet-4-6",
		DisplayName: "Claude Sonnet 4.6 (Thinking)", ContextWindow: 200000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true},
	},
	{
		ID: "antigravity/claude-opus-5-5-high", UpstreamModel: "claude-opus-5-5-high",
		DisplayName: "Claude Opus 5.5 (High)", ContextWindow: 1000000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true},
	},
	{
		ID: "antigravity/claude-sonnet-5-5-high", UpstreamModel: "claude-sonnet-5-5-high",
		DisplayName: "Claude Sonnet 5.5 (High)", ContextWindow: 1000000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true},
	},
	{
		ID: "antigravity/gemini-4-argon-high", UpstreamModel: "gemini-4-argon-high",
		DisplayName: "Gemini 4 Argon", ContextWindow: 1000000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true},
	},
	{
		ID: "antigravity/gemini-3.8-flash-high", UpstreamModel: "gemini-3.8-flash-high",
		DisplayName: "Gemini 3.8 Flash", ContextWindow: 1000000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true},
	},
	{
		ID: "antigravity/gemini-3-flash", UpstreamModel: "gemini-3-flash",
		DisplayName: "Gemini 3 Flash", ContextWindow: 1000000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true},
	},
	{
		ID: "antigravity/gemini-3.1-flash-image", UpstreamModel: "gemini-3.1-flash-image",
		DisplayName: "Gemini 3.1 Flash Image", ContextWindow: 1000000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Vision: true, Streaming: true},
	},
	{
		ID: "antigravity/gpt-oss-120b-medium", UpstreamModel: "gpt-oss-120b-medium",
		DisplayName: "GPT-OSS 120B (Medium)", ContextWindow: 128000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true},
	},
	{
		ID: "antigravity/gemini-3.1-flash-lite", UpstreamModel: "gemini-3.1-flash-lite",
		DisplayName: "Gemini 3.1 Flash Lite", ContextWindow: 1000000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Vision: true, Streaming: true},
	},
	{
		ID: "antigravity/gemini-3.5-flash-lite", UpstreamModel: "gemini-3.5-flash-lite",
		DisplayName: "Gemini 3.5 Flash Lite", ContextWindow: 1000000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Vision: true, Streaming: true},
	},
}

// Manifest 声明渠道元数据与登录方式。
func (p *Provider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:            "antigravity",
		DisplayName:   "Antigravity",
		Family:        "antigravity",
		RegionTag:     "GLOBAL",
		BrandColor:    "#F59E0B",
		Description:   "Antigravity 渠道（Google Cloud Code Assist）。支持从本机客户端凭据管理器导入，或 Google OAuth / 手动令牌登录；模型可用 antigravity/<模型名>。",
		NetworkPolicy: provider.NetPolicyProxyFirst,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthLocalImport,
			provider.AuthOAuthPKCE,
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{
				Key: "access_token", Label: "Access Token", Type: "password", Required: true,
				Placeholder: "ya29...",
				Help:        "Google OAuth access_token；本机导入会自动填好",
			},
			{
				Key: "refresh_token", Label: "Refresh Token", Type: "password",
				Placeholder: "1//...",
				Help:        "有 refresh_token 时，网关会在过期前自动续期",
			},
			{
				Key: "project_id", Label: "GCP Project（可选）", Type: "text",
				Placeholder: "aicode-consumers",
				Help:        "留空时首次请求会自动调用 loadCodeAssist 发现",
			},
		},
		OAuth: &provider.OAuthConfig{
			AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL:     tokenEndpoint,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Scopes:       oauthScopes,
			RedirectPath: "/oauth-callback",
			ExtraAuthParams: map[string]string{
				"access_type": "offline",
				"prompt":      "consent",
			},
		},
		Models:              manifestModels,
		RequiredMiddlewares: []string{"stateful_session"},
		Capabilities: provider.CapabilitySet{
			Chat:        true,
			Quota:       true,
			LocalImport: true,
			LocalInject: true,
			Refresh:     true,
		},
	}
}
