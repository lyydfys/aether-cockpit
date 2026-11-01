// Package codebuddy 提供 CodeBuddy（腾讯）国际/国内两个渠道的账号接入。
//
// 登录：auth/state → 浏览器登录 → auth/token 轮询；
// 对话：/v2/chat/completions（OpenAI 兼容 SSE）+ 官方 CLI 指纹头。
// 额度接口暂未接线。
package codebuddy

import (
	"fmt"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

type profile struct {
	id          string
	displayName string
	regionTag   string
	baseURL     string
	brandColor  string
	description string
}

var intlProfile = profile{
	id:          "codebuddy-intl",
	displayName: "CodeBuddy",
	regionTag:   "GLOBAL",
	baseURL:     "https://www.codebuddy.ai",
	brandColor:  "#2F6BFF",
	description: "腾讯 CodeBuddy 国际版：登录页授权或粘贴 Token；对话走 /v2/chat/completions（OpenAI 兼容 SSE），国际站要求首条消息为 system。",
}

var cnProfile = profile{
	id:          "codebuddy-cn",
	displayName: "CodeBuddy CN",
	regionTag:   "CN",
	baseURL:     "https://www.codebuddy.cn",
	brandColor:  "#2F6BFF",
	description: "腾讯 CodeBuddy 国内版：登录页授权或粘贴 Token；对话走 /v2/chat/completions（OpenAI 兼容 SSE）。",
}

func init() {
	provider.Register(newProvider(intlProfile))
	provider.Register(newProvider(cnProfile))
}

// Provider 实现 CodeBuddy 渠道。
type Provider struct {
	profile profile
}

func newProvider(p profile) *Provider { return &Provider{profile: p} }

// Profile 返回静态配置（测试与诊断用）。
func (p *Provider) Profile() profile { return p.profile }

func (p *Provider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:            p.profile.id,
		DisplayName:   p.profile.displayName,
		Family:        "codebuddy",
		RegionTag:     p.profile.regionTag,
		BrandColor:    p.profile.brandColor,
		Description:   p.profile.description,
		NetworkPolicy: provider.NetPolicyProxyFirst,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{
				Key: "token", Label: "Access Token", Type: "password", Required: true,
				Placeholder: "登录后从插件存储读取，或使用「CodeBuddy 登录」",
				Help:        "登录页授权会自动写入；手动粘贴用于恢复已有账号",
			},
			{Key: "refresh_token", Label: "Refresh Token（可选）", Type: "password"},
		},
		Models: []ir.ModelMeta{
			{ID: p.profile.id + "/glm-5.2", UpstreamModel: "glm-5.2", DisplayName: "GLM 5.2"},
			{ID: p.profile.id + "/deepseek-v4-pro", UpstreamModel: "deepseek-v4-pro", DisplayName: "DeepSeek V4 Pro"},
		},
		RequiredMiddlewares: []string{"stateful_session"},
		Capabilities: provider.CapabilitySet{
			Chat:    true,
			Refresh: true,
		},
	}
}

// Validate 校验 providerID 是否属于本包。
func Validate(providerID string) (*Provider, error) {
	for _, p := range []*Provider{newProvider(intlProfile), newProvider(cnProfile)} {
		if p.profile.id == providerID {
			return p, nil
		}
	}
	return nil, fmt.Errorf("渠道 %s 不是 CodeBuddy", providerID)
}

// Family 判断 manifest family（供登录校验）。
func (p *Provider) Family() string { return "codebuddy" }

var _ provider.BaseProvider = (*Provider)(nil)
var _ provider.ChatCapability = (*Provider)(nil)
