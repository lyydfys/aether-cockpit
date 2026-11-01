// Package typesafe 实现 TypeSafe Jev 决策渠道（System One）。
//
// Jev 不是对话模型：请求发往 {base}/v1/systemone，body 由调用方原样给出
// （model 固定 jev），网关只做鉴权、选号与转发；不支持流式。
package typesafe

import (
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const (
	channelID   = "typesafe"
	defaultBase = "https://api.typesafe.ai"
)

func init() {
	provider.Register(&Provider{})
}

// Provider 只提供账号与元数据；转发在网关的 /v1/systemone 处理器里完成。
type Provider struct{}

// DefaultBase 是官方地址（供文档与诊断使用）。
func DefaultBase() string { return defaultBase }

func (p *Provider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID: channelID, DisplayName: "TypeSafe Jev", Family: "typesafe", RegionTag: "GLOBAL", BrandColor: "#0F766E",
		Description:   "TypeSafe Jev 决策 API（System One）。配置 API Key 后，POST /v1/systemone 会原样转发到 TypeSafe；模型固定 jev，不支持流式。",
		NetworkPolicy: provider.NetPolicyProxyFirst,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{
				Key: "base_url", Label: "Base URL", Type: "text", Placeholder: defaultBase,
				Help: "留空使用官方地址",
			},
			{
				Key: "api_key", Label: "API Key", Type: "password", Required: true,
			},
		},
		Models: []ir.ModelMeta{
			{ID: channelID + "/jev", UpstreamModel: "jev", DisplayName: "Jev", ContextWindow: 32768,
				Capabilities: ir.ModelCapabilities{}},
		},
		Capabilities: provider.CapabilitySet{},
	}
}

var _ provider.BaseProvider = (*Provider)(nil)
