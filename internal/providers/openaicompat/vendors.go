package openaicompat

import (
	"strings"

	"aether-cockpit/internal/core/channels"
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// defaultConfig 是通用的「OpenAI 兼容端点」，行为与旧版一致。
var defaultConfig = Config{
	ID: "openai-compat", DisplayName: "OpenAI 兼容端点", Family: "generic", RegionTag: "ANY", BrandColor: "#0EA5E9",
	Description: "接入任意标准 OpenAI 协议端点：官方 API（DeepSeek/Kimi/GLM）、本地推理（Ollama/vLLM）、中转服务。模型名直接透传，无需逐个登记。",
	Models: []ir.ModelMeta{
		{
			ID: "openai-compat/gpt-4o", UpstreamModel: "gpt-4o", DisplayName: "GPT-4o", ContextWindow: 128000,
			Capabilities: ir.ModelCapabilities{ToolCall: true, Vision: true, Streaming: true},
		},
		{
			ID: "openai-compat/deepseek-chat", UpstreamModel: "deepseek-chat", DisplayName: "DeepSeek Chat", ContextWindow: 64000,
			Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true},
		},
		{
			ID: "openai-compat/deepseek-reasoner", UpstreamModel: "deepseek-reasoner", DisplayName: "DeepSeek Reasoner", ContextWindow: 64000,
			Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true},
		},
	},
}

var azureConfig = Config{
	ID: "azure", DisplayName: "Azure OpenAI", Family: "azure", RegionTag: "GLOBAL", BrandColor: "#0078D4",
	Description: "Azure OpenAI：资源 Endpoint 由用户填写，部署名作为模型（azure/<deployment>）；支持资源 api-key 或 Entra ID 应用凭据（tenant/client/secret 自动换 Bearer）。",
	AuthStyle:   "api-key", Azure: true, APIVersion: "2024-10-21",
}

func init() {
	provider.Register(&Provider{cfg: defaultConfig})
	provider.Register(&Provider{cfg: azureConfig})
	for _, v := range channels.Vendors() {
		cfg := Config{
			ID:          v.ID,
			DisplayName: v.DisplayName,
			Family:      v.Family,
			RegionTag:   v.RegionTag,
			BrandColor:  v.BrandColor,
			Description: v.Description,
			BaseURL:     v.BaseURL,
			NoKey:       v.NoKey,
			QuotaKind:   v.QuotaKind,
			NoQuota:     v.NoQuota,
			Models:      v.Models,
		}
		if v.ID == "bedrock" {
			cfg.SigV4Service = "bedrock"
		}
		for _, region := range v.Regions {
			if strings.TrimSpace(region.Anthropic) != "" {
				cfg.AllowAnthropicUpstream = true
			}
			if strings.TrimSpace(region.Responses) != "" {
				cfg.AllowResponsesUpstream = true
			}
		}
		// Command Code 的 Claude 模型只提供 /messages 端点。
		if v.ID == "commandcode" {
			cfg.AllowAnthropicUpstream = true
		}
		// xAI Grok CLI 走公开 OAuth 设备码；其余 vendor 仍是 API Key。
		if v.ID == "xai" {
			// xAI 同时提供 Responses API（与 OpenAI 同形），
			// 账号可选 api_protocol=responses。
			cfg.AllowResponsesUpstream = true
			cfg.OAuth = &provider.OAuthConfig{
				DeviceCodeURL: xaiDeviceCodeURL,
				TokenURL:      xaiTokenURL,
				ClientID:      xaiClientID,
				Scopes:        xaiScopes,
			}
			provider.Register(&xaiProvider{Provider{cfg: cfg}})
			continue
		}
		provider.Register(&Provider{cfg: cfg})
	}
}
