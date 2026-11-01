// Package qoder 实现 Qoder CN / Intl 渠道。
//
// 协议来自官方客户端与开源实现（pi-provider-qoder、magpie qoder）：
//   - 聊天：POST <base>/algo/api/v2/service/pro/sse/agent_chat_generation
//   - 请求体先按 Qoder 自定义 Base64 编码，再用 COSY 信封签名；
//   - 响应是 SSE 信封，body 内是 OpenAI ChatCompletionChunk。
//
// 本包同时支持从桌面客户端 auth.v1.dat（Electron safeStorage + DPAPI）导入，
// 以及设备令牌刷新。
package qoder

import (
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func init() {
	provider.Register(newProvider(cnProfile))
	provider.Register(newProvider(intlProfile))
}

type profile struct {
	id          string
	displayName string
	family      string
	regionTag   string
	brandColor  string
	description string
	network     provider.NetworkPolicy

	baseURL  string
	openAPI  string
	clientID string
	cosyVer  string
	authDir  string // ~/.qoder-cn 或 ~/.qoder
	appDir   string // Windows 凭据目录名
	models   []string
}

var cnProfile = profile{
	id:          "qoder-cn",
	displayName: "Qoder CN",
	family:      "qoder",
	regionTag:   "CN",
	brandColor:  "#8B5CF6",
	description: "Qoder 国内版。直连 gateway.qoder.com.cn 的 agent_chat_generation，COSY 签名 + 自定义 Base64 编码；支持从 Qoder CN 桌面客户端导入登录态。",
	network:     provider.NetPolicyDirectForce,
	baseURL:     "https://gateway.qoder.com.cn",
	openAPI:     "https://openapi.qoder.com.cn",
	clientID:    "e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb",
	cosyVer:     "1.1.77",
	authDir:     ".qoder-cn",
	appDir:      "com.qodercn.app.stable",
	models: []string{
		"auto", "Qwen3.8-Max", "Qwen3.5-Plus", "GLM-5.2", "GLM-5.1",
		"DeepSeek-V4.1-Pro", "DeepSeek-V4.1-Flash",
		"DeepSeek-V4-Pro", "DeepSeek-V4-Flash", "Kimi-K2", "MiniMax-M3",
		"Ultimate", "Performance", "Efficient",
	},
}

var intlProfile = profile{
	id:          "qoder-intl",
	displayName: "Qoder Intl",
	family:      "qoder",
	regionTag:   "GLOBAL",
	brandColor:  "#A78BFA",
	description: "Qoder 国际版。直连 api3.qoder.sh 的 agent_chat_generation，COSY 签名 + 自定义 Base64 编码；支持从 Qoder 桌面客户端导入登录态。",
	network:     provider.NetPolicyProxyFirst,
	baseURL:     "https://api3.qoder.sh",
	openAPI:     "https://openapi.qoder.sh",
	clientID:    "732aef47-9cf2-46a2-95fe-4cebb5d0d1fa",
	cosyVer:     "1.1.77",
	authDir:     ".qoder",
	appDir:      "com.qoder.app.stable",
	models: []string{
		"auto", "Qwen3.8-Max", "Qwen3.5-Plus", "GLM-5.2", "GLM-5.1",
		"DeepSeek-V4.1-Pro", "DeepSeek-V4.1-Flash",
		"DeepSeek-V4-Pro", "DeepSeek-V4-Flash", "Kimi-K2", "MiniMax-M3",
		"Ultimate", "Performance", "Efficient",
	},
}

type Provider struct {
	profile profile
}

func newProvider(p profile) *Provider { return &Provider{profile: p} }

// Profile 返回静态配置（测试与诊断用）。
func (p *Provider) Profile() profile { return p.profile }

// Manifest 声明渠道元数据。
func (p *Provider) Manifest() provider.ProviderManifest {
	models := make([]ir.ModelMeta, 0, len(p.profile.models))
	for _, name := range p.profile.models {
		models = append(models, ir.ModelMeta{
			ID:            p.profile.id + "/" + name,
			UpstreamModel: name,
			DisplayName:   name,
			Capabilities:  ir.ModelCapabilities{Streaming: true, ToolCall: true, Reasoning: true},
		})
	}
	return provider.ProviderManifest{
		ID:            p.profile.id,
		DisplayName:   p.profile.displayName,
		Family:        p.profile.family,
		RegionTag:     p.profile.regionTag,
		BrandColor:    p.profile.brandColor,
		Description:   p.profile.description,
		NetworkPolicy: p.profile.network,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthLocalImport,
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{
				Key: "token", Label: "设备令牌 / PAT", Type: "password", Required: true,
				Placeholder: "dt-... / qoder_...",
				Help:        "桌面客户端导入会自动填好；也可粘贴 Personal Access Token",
			},
			{
				Key: "machine_id", Label: "Machine ID", Type: "text", Required: true,
				Help: "COSY 签名必需；本机客户端导入会自动填好",
			},
			{
				Key: "user_id", Label: "User ID", Type: "text",
				Help: "可选；留空时从 token/userinfo 推断",
			},
			{
				Key: "refresh_token", Label: "Refresh Token", Type: "password",
				Help: "可选；用于设备令牌自动续期",
			},
			{
				Key: "base_url", Label: "Base URL（可选）", Type: "text",
				Placeholder: p.profile.baseURL,
				Help:        "留空用官方网关；走自建反代时填写，COSY 签名会按该地址重新计算",
			},
		},
		Models:              models,
		RequiredMiddlewares: []string{"stateful_session"},
		Tasks: []provider.TaskDefinition{
			{TaskID: "daily_checkin", Title: "每日积分", CronDefault: "0 9 * * *"},
		},
		Capabilities: provider.CapabilitySet{
			Chat:        true,
			LocalImport: true,
			LocalInject: localInjectSupported,
			Refresh:     true,
			CheckIn:     true,
		},
	}
}
