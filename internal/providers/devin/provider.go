// Package devin 接入 Devin CLI（app.devin.ai）渠道。
//
// Devin CLI 的推理后端是 Codeium/Devin 的 Connect-RPC 服务
// （server.codeium.com/exa.api_server_pb.ApiServerService/GetChatMessage），
// 不是 OpenAI 兼容协议：请求体是 protobuf + Connect 帧，鉴权头是
// `Basic <token>-<token>`，token 为 Devin CLI 登录产出的
// `devin-session-token$...`。
//
// 本渠道只做「渠道反代」：粘贴 session token 即可对话；不接管 Devin CLI
// 本机配置，也不做浏览器 OAuth。
package devin

import (
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const channelID = "devin"

func init() {
	provider.Register(&Provider{})
}

// Provider 实现 Devin CLI 渠道。
type Provider struct{}

// Manifest 声明渠道元数据。
func (p *Provider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:            channelID,
		DisplayName:   "Devin CLI",
		Family:        "devin",
		RegionTag:     "GLOBAL",
		BrandColor:    "#0B7A5C",
		Description:   "Devin CLI（app.devin.ai）：粘贴 devin-session-token$... 后走 Connect-RPC 反代对话；协议对齐官方 CLI（Basic token-token、connect+proto 流、swe-2 等上游模型 UID）。",
		NetworkPolicy: provider.NetPolicyProxyFirst,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{
				Key: "session_token", Label: "Session Token", Type: "password", Required: true,
				Placeholder: "devin-session-token$...",
				Help:        "Devin CLI 登录后 auth 记录里的 session token；以 eyJ 开头的 JWT 会自动补前缀",
			},
			{Key: "base_url", Label: "Base URL（可选）", Type: "text", Placeholder: devinDefaultBaseURL},
			{Key: "device_seed", Label: "设备指纹种子（可选）", Type: "text", Help: "留空则每次随机生成 732 位十六进制指纹"},
		},
		Models: []ir.ModelMeta{
			{ID: "devin/swe-2", UpstreamModel: "swe-2-high", DisplayName: "SWE-2"},
			{ID: "devin/claude-sonnet-4-6", UpstreamModel: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6"},
			{ID: "devin/gpt-5.2", UpstreamModel: "gpt-5.2", DisplayName: "GPT-5.2"},
			{ID: "devin/glm-5.2", UpstreamModel: "glm-5.2", DisplayName: "GLM 5.2"},
		},
		Capabilities: provider.CapabilitySet{
			Chat: true,
		},
	}
}

var (
	_ provider.BaseProvider   = (*Provider)(nil)
	_ provider.ChatCapability = (*Provider)(nil)
)
