// Package workbuddy 实现 WorkBuddy CN / Intl 渠道。
//
// WorkBuddy（CodeBuddy 桌面端）的对话接口是标准 OpenAI Chat Completions
// 流式端点，差异只在端点域名与一组自定义头（X-Domain / X-User-Id /
// X-Conversation-* / User-Agent）。请求与响应体完全按 OpenAI 协议处理。
package workbuddy

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
	domain   string
	infoFile string
	intl     bool
	models   []string
}

var cnProfile = profile{
	id:          "workbuddy-cn",
	displayName: "WorkBuddy CN",
	family:      "workbuddy",
	regionTag:   "CN",
	brandColor:  "#2563EB",
	description: "WorkBuddy 国内版。直连 copilot.tencent.com 的 OpenAI 兼容流式端点，附带 WorkBuddy 客户端自定义头；支持从本机 WorkBuddy 桌面端导入登录态，或手动填写 token / user_id。",
	network:     provider.NetPolicyDirectForce,
	baseURL:     "https://copilot.tencent.com/v2",
	domain:      "www.workbuddy.cn",
	infoFile:    "workbuddy-desktop.info",
	models: []string{
		"auto", "hy4-preview", "hy3", "hy3-x", "deepseek-v4.1-flash",
		"deepseek-v4-pro", "glm-5.3", "glm-5.3-flash", "glm-5.2", "glm-5.1",
		"glm-5v-turbo", "minimax-m3", "kimi-k3", "kimi-k2.7-code", "kimi-k2.6",
	},
}

var intlProfile = profile{
	id:          "workbuddy-intl",
	displayName: "WorkBuddy Intl",
	family:      "workbuddy",
	regionTag:   "GLOBAL",
	brandColor:  "#3B82F6",
	description: "WorkBuddy 国际版。直连 www.workbuddy.ai 的 OpenAI 兼容流式端点，附带 WorkBuddy 客户端自定义头；支持从本机 WorkBuddy 桌面端导入登录态，或手动填写 token / user_id。",
	network:     provider.NetPolicyProxyFirst,
	baseURL:     "https://www.workbuddy.ai/v2",
	domain:      "www.workbuddy.ai",
	infoFile:    "workbuddy-desktop-ai.info",
	intl:        true,
	// 模型 ID 来自上游 /v3/config 的 cli agent 清单。档位名必须带 -model 后缀
	// （default/fast 这类裸名会返回 11102 service info not found）。
	// DeepSeek 排在最前：它是免费模型，成本排序会优先选它，避免消耗付费额度。
	models: []string{
		"deepseek-v4.1-flash", "deepseek-v4.1-flash-sg",
		"default-model", "fast-model", "balanced-model", "primary-model",
		"gpt-5.5", "gpt-5.4", "gpt-5.3-codex", "gemini-3.1-pro", "gemini-3.5-flash",
		"glm-5.3", "glm-5.2", "hy3", "kimi-k3", "kimi-k2.6", "minimax-m3",
	},
}

// Provider 实现 WorkBuddy 渠道。
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

	// 签到活动只在国内版存在（国际版没有这个事件）。
	var tasks []provider.TaskDefinition
	capabilities := provider.CapabilitySet{Chat: true, LocalImport: true, Quota: true}
	if !p.profile.intl {
		tasks = []provider.TaskDefinition{
			{TaskID: "daily_checkin", Title: "每日签到", CronDefault: "0 9 * * *"},
		}
		capabilities.CheckIn = true
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
				Key: "token", Label: "Token", Type: "password", Required: true,
				Placeholder: "WorkBuddy accessToken",
				Help:        "WorkBuddy 桌面端登录态里的 accessToken；本机导入会自动填好",
			},
			{
				Key: "user_id", Label: "User ID", Type: "text", Required: true,
				Placeholder: "uid",
				Help:        "对应请求头 X-User-Id；本机导入会自动填好",
			},
			{
				Key: "domain", Label: "Domain（可选）", Type: "text",
				Placeholder: p.profile.domain,
				Help:        "自定义 X-Domain；留空时使用该渠道的默认域名",
			},
			{
				Key: "enterprise_id", Label: "Enterprise ID（可选）", Type: "text",
				Help: "企业版账号才需要；会作为 X-Enterprise-Id 发送",
			},
			{
				Key: "tenant_id", Label: "Tenant ID（可选）", Type: "text",
				Help: "企业版账号才需要；会作为 X-Tenant-Id 发送",
			},
		},
		Models:              models,
		RequiredMiddlewares: []string{"stateful_session"},
		Tasks:               tasks,
		Capabilities:        capabilities,
	}
}

var (
	_ provider.BaseProvider          = (*Provider)(nil)
	_ provider.QuotaCapability       = (*Provider)(nil)
	_ provider.ChatCapability        = (*Provider)(nil)
	_ provider.LocalImportCapability = (*Provider)(nil)
)
