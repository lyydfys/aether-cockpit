// Package minimax 实现 MiniMax Code 渠道（国内与国际两站）。
//
// 协议来自 aether 的社区插件 packages/minimax（mcode 0.6.12）：
//   - 对话是 Anthropic Messages：POST <llm>/mavis/api/v1/llm/v1/messages
//     Bearer 账号令牌 + MiniMax Code 的 X-Mavis-* 头；
//   - 模型清单：GET <llm>/mavis/api/v1/models（本包用内置清单）；
//   - 每日签到：<agent>/minimax-cloud/api/v1/signin/{status,claim}，
//     带 mcode 的签名参数（x-timestamp / x-signature / yy）。
//
// 目前只做"账号令牌"接入（手动粘贴 access_token）；OAuth 设备登录与本机
// MiniMax Code 登录态导入尚未实现。
package minimax

import (
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func init() {
	provider.Register(newProvider(cnProfile))
	provider.Register(newProvider(intlProfile))
}

type profile struct {
	id           string
	displayName  string
	regionTag    string
	brandColor   string
	description  string
	network      provider.NetworkPolicy
	llmBase      string
	agentBase    string
	platformBase string
	lang         string
	region       string
	osName       string
	models       []modelSpec
}

type modelSpec struct {
	id      string
	name    string
	context int
	output  int
	vision  bool
}

var modelList = []modelSpec{
	{id: "MiniMax-M3.1-Flash-Preview", name: "M3.1-Flash-Preview", context: 1048576, output: 524288, vision: true},
	{id: "MiniMax-M3", name: "MiniMax-M3", context: 1048576, output: 524288, vision: true},
	{id: "MiniMax-M2.7", name: "MiniMax-M2.7", context: 200000, output: 128000},
	{id: "MiniMax-M2.7-highspeed", name: "MiniMax-M2.7-highspeed", context: 200000, output: 128000},
}

var cnProfile = profile{
	id:           "minimax-code",
	displayName:  "MiniMax Code",
	regionTag:    "CN",
	brandColor:   "#F75C03",
	description:  "MiniMax Code 国内版。Anthropic Messages 协议直连 agent.minimax.cn，支持每日签到领积分；手动粘贴账号 access token 即可使用。",
	network:      provider.NetPolicyDirectForce,
	llmBase:      "https://agent.minimax.cn",
	agentBase:    "https://agent.minimaxi.com",
	platformBase: "https://www.minimaxi.com",
	lang:         "zh",
	region:       "cn",
	osName:       "windows",
	models:       modelList,
}

var intlProfile = profile{
	id:           "minimax-code-global",
	displayName:  "MiniMax Code (Global)",
	regionTag:    "GLOBAL",
	brandColor:   "#FF7A1A",
	description:  "MiniMax Code 国际版。Anthropic Messages 协议直连 agent.minimax.io，支持每日签到；手动粘贴账号 access token 即可使用。",
	network:      provider.NetPolicyProxyFirst,
	llmBase:      "https://agent.minimax.io",
	agentBase:    "https://agent.minimax.io",
	platformBase: "https://platform.minimax.io",
	lang:         "en",
	region:       "en",
	osName:       "windows",
	models:       modelList,
}

type Provider struct {
	profile profile
}

func newProvider(p profile) *Provider { return &Provider{profile: p} }

func (p *Provider) Profile() profile { return p.profile }

func (p *Provider) Manifest() provider.ProviderManifest {
	models := make([]ir.ModelMeta, 0, len(p.profile.models))
	for _, m := range p.profile.models {
		models = append(models, ir.ModelMeta{
			ID:            p.profile.id + "/" + m.id,
			UpstreamModel: m.id,
			DisplayName:   m.name,
			ContextWindow: m.context,
			Capabilities: ir.ModelCapabilities{
				Streaming: true,
				ToolCall:  true,
				Reasoning: true,
				Vision:    m.vision,
			},
		})
	}
	return provider.ProviderManifest{
		ID:            p.profile.id,
		DisplayName:   p.profile.displayName,
		Family:        "minimax",
		RegionTag:     p.profile.regionTag,
		BrandColor:    p.profile.brandColor,
		Description:   p.profile.description,
		NetworkPolicy: p.profile.network,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{
				Key: "access_token", Label: "Access Token", Type: "password", Required: true,
				Placeholder: "MiniMax Code 账号 access token",
				Help:        "MiniMax Code 账号的 OAuth access token；从客户端登录态或官方接口取得",
			},
			{
				Key: "user_id", Label: "User ID（可选）", Type: "text",
				Help: "签到需要 realUserID；留空时会尝试从 /v1/api/user/info 读取",
			},
			{
				Key: "platform_base_url", Label: "Plan Base URL（可选）", Type: "text",
				Placeholder: p.profile.platformBase,
				Help:        "读取 M Plan 5h/周窗口时覆盖平台地址；留空使用官方地址",
			},
			{
				Key: "base_url", Label: "聊天 Base URL（可选）", Type: "text",
				Placeholder: p.profile.llmBase,
				Help:        "自建代理或测试时覆盖；留空使用官方地址",
			},
		},
		Models:              models,
		RequiredMiddlewares: []string{"stateful_session"},
		Tasks: []provider.TaskDefinition{
			{TaskID: "daily_checkin", Title: "每日签到", CronDefault: "0 9 * * *"},
		},
		Capabilities: provider.CapabilitySet{
			Chat:    true,
			CheckIn: true,
			Quota:   true,
			Refresh: true,
		},
	}
}

var (
	_ provider.BaseProvider           = (*Provider)(nil)
	_ provider.ChatCapability         = (*Provider)(nil)
	_ provider.CheckInCapability      = (*Provider)(nil)
	_ provider.QuotaCapability        = (*Provider)(nil)
	_ provider.TokenRefreshCapability = (*Provider)(nil)
)

func accessToken(env *provider.AccountEnv) string {
	if v := env.GetCredential("access_token"); v != "" {
		return v
	}
	return env.GetCredential("token")
}
