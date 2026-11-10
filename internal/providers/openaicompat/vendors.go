package openaicompat

import (
	"strings"

	"aether-cockpit/internal/core/channels"
	"aether-cockpit/internal/provider"
)

// defaultConfig 是通用的「OpenAI 兼容端点」。
//
// **不再登记内置模型清单**（曾经塞了 gpt-4o / deepseek-chat / deepseek-reasoner 三条占位）。
// 理由：
//   - 这个渠道的定位就是"上游模型名直接透传"，模型清单应当来自上游 `/models`（catalog 的
//     live 目录），而不是在本地写死几个名字；
//   - 写死的那三条会在客户端模型列表里变成 `openai-compat/gpt-4o` 这种跟用户实际接入的
//     上游毫无关系的条目，还会被 `auto/` 自动发现组放大成 `auto/gpt-4o` —— 即"奇奇怪怪的模型名"；
//   - 空清单时 `/v1/models` 只列上游真实返回的模型，接什么上游就看到什么，不会误导。
var defaultConfig = Config{
	ID: "openai-compat", DisplayName: "OpenAI 兼容端点", Family: "generic", RegionTag: "ANY", BrandColor: "#0EA5E9",
	Description: "接入任意标准 OpenAI 协议端点：官方 API（DeepSeek/Kimi/GLM）、本地推理（Ollama/vLLM）、中转服务。模型名直接透传，无需逐个登记。",
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
		// Open Design 是中转聚合：同一个 Key 同时提供 OpenAI Chat、
		// Anthropic Messages 与 OpenAI Responses 三种端点。
		// 三个协议要各自记住自己的 Base URL，否则只能配其中一个，
		// 另外两个协议就会打到错误端点（用户在编辑器里填了却"不生效"）。
		if v.ID == "open-design" {
			cfg.AllowAnthropicUpstream = true
			cfg.AllowResponsesUpstream = true
			cfg.AnthropicBaseURL = "https://amr-link.open-design.ai"
			cfg.ResponsesBaseURL = "https://amr-link.open-design.ai/v1"
		}
		// Command Code 的 Claude 模型只提供 /messages 端点。
		if v.ID == "commandcode" {
			cfg.AllowAnthropicUpstream = true
		}
		// DeepSeek 官方除 OpenAI 兼容端点外，还提供 Anthropic Messages 端点
		// （/anthropic/v1/messages），Claude Code 这类客户端可以直接挂上来。
		if v.ID == "deepseek" {
			cfg.AllowAnthropicUpstream = true
			cfg.AnthropicBaseURL = "https://api.deepseek.com/anthropic"
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
