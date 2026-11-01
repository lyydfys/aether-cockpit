// Package kiro 实现 Kiro（AWS CodeWhisperer / Kiro Runtime）渠道。
//
// 协议来自官方 Kiro Runtime 与开源实现（pi-provider-kiro）：
//   - 运行时：POST https://runtime.<region>.kiro.dev/generateAssistantResponse
//   - 鉴权：Authorization: Bearer <access token>（ksk_ 开头时附 tokentype: API_KEY）
//   - 请求：conversationState + profileArn；
//   - 响应：AWS 二进制 EventStream，帧头 :event-type 决定事件类型。
package kiro

import (
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func init() {
	provider.Register(&Provider{})
}

// Provider 实现 Kiro 渠道。
type Provider struct{}

var manifestModels = []struct {
	id      string
	display string
	up      string
}{
	{"claude-opus-4-8", "Claude Opus 4.8", "claude-opus-4.8"},
	{"claude-opus-4-7", "Claude Opus 4.7", "claude-opus-4.7"},
	{"claude-opus-4-6", "Claude Opus 4.6", "claude-opus-4.6"},
	{"claude-sonnet-5", "Claude Sonnet 5", "claude-sonnet-5"},
	{"claude-sonnet-4-6", "Claude Sonnet 4.6", "claude-sonnet-4.6"},
	{"claude-sonnet-4-5", "Claude Sonnet 4.5", "claude-sonnet-4.5"},
	{"claude-sonnet-4", "Claude Sonnet 4", "claude-sonnet-4"},
	{"claude-haiku-4-5", "Claude Haiku 4.5", "claude-haiku-4.5"},
	{"claude-fable-5", "Claude Fable 5", "claude-fable-5"},
	{"deepseek-3-2", "DeepSeek 3.2", "deepseek-3.2"},
	{"minimax-m2-5", "MiniMax M2.5", "minimax-m2.5"},
	{"minimax-m2-1", "MiniMax M2.1", "minimax-m2.1"},
	{"glm-5", "GLM 5", "glm-5"},
	{"qwen3-coder-next", "Qwen3 Coder Next", "qwen3-coder-next"},
	{"gpt-5-6-sol", "GPT-5.6 Sol", "gpt-5.6-sol"},
	{"gpt-5-6-terra", "GPT-5.6 Terra", "gpt-5.6-terra"},
	{"gpt-5-6-luna", "GPT-5.6 Luna", "gpt-5.6-luna"},
	{"auto", "Auto", "auto"},
}

// Manifest 声明渠道元数据。
func (p *Provider) Manifest() provider.ProviderManifest {
	models := make([]ir.ModelMeta, 0, len(manifestModels))
	for _, m := range manifestModels {
		models = append(models, ir.ModelMeta{
			ID:            "kiro/" + m.id,
			UpstreamModel: m.up,
			DisplayName:   m.display,
			Capabilities:  ir.ModelCapabilities{Streaming: true, ToolCall: true, Reasoning: true},
		})
	}
	return provider.ProviderManifest{
		ID:            "kiro",
		DisplayName:   "Kiro",
		Family:        "kiro",
		RegionTag:     "GLOBAL",
		BrandColor:    "#F97316",
		Description:   "Kiro 渠道（AWS Kiro Runtime）。直连 runtime.<region>.kiro.dev/generateAssistantResponse，AWS EventStream 解码；支持手动令牌与本机账号导入。",
		NetworkPolicy: provider.NetPolicyProxyFirst,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthManualInput,
			provider.AuthLocalImport,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{
				Key: "access_token", Label: "Access Token / API Key", Type: "password", Required: true,
				Placeholder: "eyJ... / ksk_...",
				Help:        "Kiro CLI 或 Builder ID 的 access token；ksk_ 开头的 API Key 也可以",
			},
			{
				Key: "profile_arn", Label: "Profile ARN", Type: "text", Required: true,
				Placeholder: "arn:aws:codewhisperer:us-east-1:...:profile/...",
				Help:        "Kiro Runtime 必需；可从 Kiro CLI 登录态或 ListAvailableProfiles 获取",
			},
			{
				Key: "region", Label: "Region", Type: "text",
				Placeholder: "us-east-1",
				Help:        "留空默认 us-east-1；profile ARN 的 region 优先",
			},
		},
		Models:              models,
		RequiredMiddlewares: []string{"stateful_session"},
		Capabilities: provider.CapabilitySet{
			Chat:        true,
			LocalImport: true,
			Quota:       true,
		},
	}
}
