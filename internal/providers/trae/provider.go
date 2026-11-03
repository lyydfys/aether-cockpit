// Package trae 是 Trae IDE / SOLO 的真实渠道实现（CN 与 Intl 两个区域变体）。
//
// 协议要点（与官方客户端一致）：
//   - 对话：POST /api/agent/v3/llm_utils_chat，请求体为
//     {messages, function:"chat_v3", stream, model}；不带 config_name；
//   - 鉴权：Cloud-IDE-JWT <token> + X-Cloudide-Token，另需一整套 IDE 设备头；
//   - User-Agent 必须是 Trae/<版本>，否则上游会以 4011 限流拒绝；
//   - 流式：text/event-stream，event 名区分 output / done / token_usage / error；
//   - 工具：上游没有原生 tools 字段，历史工具调用被压平为 <toolcall>/<tool_result> 文本。
//
// 本包负责把 IR 请求翻译成 Trae 请求、把 SSE 还原成 IR 极简事件，
// 并支持从本机 Trae 客户端的 storage.json 导入登录态（含 tc 加密凭证解密）。
package trae

import (
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func init() {
	provider.Register(newProvider("trae-cn", "CN"))
	provider.Register(newProvider("trae-intl", "SG"))
	provider.Register(newProvider("trae-solo-cn", "CN"))
	provider.Register(newProvider("trae-solo", "SG"))
}

// Provider 是 Trae 渠道实现。CN 与 Intl 共用同一套协议，仅区域、
// 网络策略、上游 Host 与模型清单不同。
type Provider struct {
	id     string
	region string // CN | SG
	solo   bool
}

func newProvider(id, region string) *Provider {
	return &Provider{id: id, region: region, solo: strings.Contains(id, "solo")}
}

// modelSpec 是静态模型清单的声明式条目。
type modelSpec struct {
	name      string
	display   string
	reasoning bool
	vision    bool
}

var cnModels = []modelSpec{
	{name: "Doubao-Seed-Code", display: "Doubao Seed Code", vision: true},
	{name: "glm-5.2", display: "GLM-5.2", reasoning: true},
	{name: "minimax-m3", display: "MiniMax-M3", reasoning: true, vision: true},
	{name: "qwen-3.7-plus", display: "Qwen3.7-Plus", reasoning: true, vision: true},
	{name: "DeepSeek-V4-Pro", display: "DeepSeek-V4-Pro", reasoning: true},
	{name: "DeepSeek-V4-Flash", display: "DeepSeek-V4-Flash", reasoning: true},
	// 上游同时接受小写的新版本名（实测 200），列出来才能被裸名
	// deepseek-v4.1-flash 路由到。
	{name: "deepseek-v4.1-flash", display: "DeepSeek V4.1 Flash", reasoning: true},
	{name: "glm-5.1", display: "GLM-5.1", reasoning: true},
	{name: "glm-4.7", display: "GLM-4.7", reasoning: true},
	{name: "kimi-k2", display: "Kimi-K2-0905"},
	{name: "qwen3-coder", display: "Qwen3-Coder-Next"},
	{name: "doubao-for-auto", display: "Doubao Auto", vision: true},
}

var sgModels = []modelSpec{
	{name: "gpt-5.6", display: "GPT-5.6", reasoning: true, vision: true},
	{name: "gpt-5.6-terra", display: "GPT-5.6 Terra", reasoning: true, vision: true},
	{name: "gpt-5.6-luna", display: "GPT-5.6 Luna", reasoning: true, vision: true},
	{name: "gemini-3-flash", display: "Gemini 3 Flash", reasoning: true, vision: true},
	{name: "kimi-k3", display: "Kimi-K3", reasoning: true},
	{name: "minimax-m3", display: "MiniMax-M3", reasoning: true, vision: true},
	{name: "deepseek-v3", display: "DeepSeek-V3", reasoning: true},
	{name: "doubao-seed-1.6", display: "Doubao Seed 1.6", vision: true},
}

// defaultChatModel 返回 auto 映射到的具体模型。
// inline_chat 在当前上游不可用，auto 只能退到 chat_v3 的具体模型。
func (p *Provider) defaultChatModel() string {
	if p.region == "CN" {
		return "glm-5.1"
	}
	return "gpt-5.6"
}

// buildModels 生成对外模型清单。auto 仍保留，但 ChatStream 会把它
// 映射到 defaultChatModel 的 chat_v3 请求。
func buildModels(providerID string, specs []modelSpec) []ir.ModelMeta {
	models := make([]ir.ModelMeta, 0, len(specs)+1)
	models = append(models, ir.ModelMeta{
		ID:            providerID + "/auto",
		UpstreamModel: "auto",
		DisplayName:   "Auto (默认模型)",
		Capabilities:  ir.ModelCapabilities{ToolCall: true, Streaming: true},
	})
	for _, spec := range specs {
		models = append(models, ir.ModelMeta{
			ID:            providerID + "/" + spec.name,
			UpstreamModel: spec.name,
			DisplayName:   spec.display,
			Capabilities: ir.ModelCapabilities{
				ToolCall:  true,
				Reasoning: spec.reasoning,
				Vision:    spec.vision,
				Streaming: true,
			},
		})
	}
	return models
}

// Manifest 声明渠道元数据、登录方式与模型清单。
func (p *Provider) Manifest() provider.ProviderManifest {
	cn := p.region == "CN"
	displayName := "Trae Intl (SG)"
	regionTag := "GLOBAL"
	brandColor := "#38BDF8"
	description := "Trae 国际版（SG）真实渠道，直连 https://coresg-normal.trae.ai/api/agent/v3/llm_utils_chat（凭据 region=US 时切到 coreva）。支持从本机 Trae IDE / SOLO 的 storage.json 自动导入登录态（含 tc 加密凭证解密），或手动粘贴 token。模型可用 trae-intl/<模型名>。"
	networkPolicy := provider.NetPolicyProxyFirst
	models := buildModels(p.id, sgModels)
	if cn {
		displayName = "Trae CN"
		regionTag = "CN"
		brandColor = "#0EA5E9"
		description = "Trae 国内版真实渠道，直连 https://trae-api-cn.mchost.guru/api/agent/v3/llm_utils_chat。支持从本机 Trae IDE / SOLO 的 storage.json 自动导入登录态（含 tc 加密凭证解密），或手动粘贴 token。模型可用 trae-cn/<模型名>。"
		networkPolicy = provider.NetPolicyDirectForce
		models = buildModels(p.id, cnModels)
	}
	if p.solo {
		if cn {
			displayName = "TRAE SOLO CN"
		} else {
			displayName = "TRAE SOLO"
		}
		description = "Trae SOLO（智能体工作台）真实渠道，复用 Trae 协议与登录态；支持本机导入/手动 Token。"
	}

	// 签到活动只有 Trae CN 有；国际版没有。
	var tasks []provider.TaskDefinition
	capabilities := provider.CapabilitySet{Chat: true, LocalImport: true, LocalInject: true}
	if cn {
		tasks = []provider.TaskDefinition{
			{TaskID: "daily_checkin", Title: "每日签到", CronDefault: "0 9 * * *"},
		}
		capabilities.CheckIn = true
		capabilities.Quota = true
	}

	return provider.ProviderManifest{
		ID:            p.id,
		DisplayName:   displayName,
		Family:        "trae",
		RegionTag:     regionTag,
		BrandColor:    brandColor,
		Description:   description,
		NetworkPolicy: networkPolicy,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthLocalImport,
			provider.AuthManualInput,
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{
				Key: "token", Label: "Token", Type: "password", Required: true,
				Placeholder: "eyJ...",
				Help:        "Trae 登录 JWT；本机导入会自动填好",
			},
			{
				Key: "user_id", Label: "User ID", Type: "text",
				Help: "来自 storage.json 的 userId，用于 x-uid 头",
			},
			{
				Key: "machine_id", Label: "Machine ID", Type: "text",
				Help: "telemetry.machineId；缺失时会用零值兜底",
			},
			{
				Key: "device_id", Label: "Device ID", Type: "text",
				Help: "iCubeAuthInfo://icube-dc 数字设备号；留空时由 machine_id 哈希生成",
			},
			{
				Key: "region", Label: "Region", Type: "select",
				Options: []string{"CN", "SG", "US"},
				Help:    "国际渠道可填 US 切换到 coreva 上游",
			},
		},
		Models:              models,
		RequiredMiddlewares: []string{"stateful_session"},
		Tasks:               tasks,
		Capabilities:        capabilities,
	}
}

// 编译期确认实现了对话与本机导入能力。
var (
	_ provider.ChatCapability        = (*Provider)(nil)
	_ provider.LocalImportCapability = (*Provider)(nil)
	_ provider.LocalInjectCapability = (*Provider)(nil)
)

var _ provider.QuotaCapability = (*Provider)(nil)
