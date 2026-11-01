// Package channels 是「一等公民厂商」与「添加供应商预设」的单一数据源。
//
// 平台清单参照 relay-panel 的 platformList：每个厂商登记一条，接入只需 Key，
// Base URL 可被账号覆盖。网关用它渲染 presets，OpenAI 兼容渠道用它注册
// 各自的 Provider，避免两处各写一份。
package channels

import "aether-cockpit/internal/core/ir"

// Vendor 描述一个 OpenAI 兼容的一等公民渠道及其预设信息。
type Vendor struct {
	ID          string
	DisplayName string
	Family      string
	RegionTag   string
	BrandColor  string
	Description string
	BaseURL     string
	NoKey       bool
	// QuotaKind 声明该厂商的额度读取方式（目前 "cline" / "commandcode"）。
	QuotaKind string
	Models    []ir.ModelMeta
	// Regions 是同一厂商的多个套餐/区域端点（aether KeyPlans）：编辑器
	// 用区域选择器切换，保存时把选中的 Base URL 写进账号。
	Regions     []Region
	RegionLabel string
	// 预设（aether 添加供应商弹层）
	Kind    string // vendor / relay / local
	Icon    string
	Website string
	KeysURL string
	Note    string
	Short   string
}

// Region 是一个套餐/区域的端点组。
type Region struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Chat      string `json:"chat,omitempty"`
	Responses string `json:"responses,omitempty"`
	Anthropic string `json:"anthropic,omitempty"`
	KeysURL   string `json:"keysUrl,omitempty"`
	Website   string `json:"website,omitempty"`
}

var vendors = []Vendor{
	{
		ID: "openai", DisplayName: "OpenAI", Family: "openai", RegionTag: "GLOBAL", BrandColor: "#10A37F",
		Description: "OpenAI 官方 Chat Completions API；模型名直接透传。",
		BaseURL:     "https://api.openai.com/v1",
		Kind:        "vendor", Icon: "openai",
		Website: "https://platform.openai.com", KeysURL: "https://platform.openai.com/api-keys",
		Models: []ir.ModelMeta{
			{ID: "openai/gpt-4o", UpstreamModel: "gpt-4o", DisplayName: "GPT-4o", ContextWindow: 128000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Vision: true, Streaming: true}},
			{ID: "openai/gpt-4o-mini", UpstreamModel: "gpt-4o-mini", DisplayName: "GPT-4o mini", ContextWindow: 128000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Vision: true, Streaming: true}},
			{ID: "openai/o3-mini", UpstreamModel: "o3-mini", DisplayName: "o3-mini", ContextWindow: 200000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true}},
		},
	},
	{
		ID: "deepseek", DisplayName: "DeepSeek", Family: "deepseek", RegionTag: "CN", BrandColor: "#4D6BFE",
		Description: "DeepSeek 开放平台（OpenAI 兼容）。",
		BaseURL:     "https://api.deepseek.com/v1", QuotaKind: "deepseek",
		Kind: "vendor", Icon: "deepseek-color",
		Website: "https://platform.deepseek.com", KeysURL: "https://platform.deepseek.com/api_keys",
		Models: []ir.ModelMeta{
			{ID: "deepseek/deepseek-chat", UpstreamModel: "deepseek-chat", DisplayName: "DeepSeek Chat", ContextWindow: 128000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
			{ID: "deepseek/deepseek-reasoner", UpstreamModel: "deepseek-reasoner", DisplayName: "DeepSeek Reasoner", ContextWindow: 128000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true}},
		},
	},
	{
		ID: "xai", DisplayName: "xAI", Family: "xai", RegionTag: "GLOBAL", BrandColor: "#111827",
		Description: "xAI Grok（OpenAI 兼容 chat + Responses）。",
		BaseURL:     "https://api.x.ai/v1",
		Kind:        "vendor", Icon: "xai",
		Website: "https://console.x.ai", KeysURL: "https://console.x.ai",
		Models: []ir.ModelMeta{
			{ID: "xai/grok-4", UpstreamModel: "grok-4", DisplayName: "Grok 4", ContextWindow: 256000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true}},
			{ID: "xai/grok-3-mini", UpstreamModel: "grok-3-mini", DisplayName: "Grok 3 mini", ContextWindow: 131072,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true}},
		},
	},
	{
		ID: "moonshot", DisplayName: "Kimi", Family: "moonshot", RegionTag: "GLOBAL", BrandColor: "#7C3AED",
		Description: "Moonshot / Kimi 开放平台（OpenAI 兼容）。",
		BaseURL:     "https://api.moonshot.ai/v1", QuotaKind: "moonshot",
		Kind: "vendor", Icon: "kimi",
		Website: "https://platform.kimi.ai", KeysURL: "https://platform.kimi.ai/console/api-keys",
		Models: []ir.ModelMeta{
			{ID: "moonshot/kimi-k2", UpstreamModel: "kimi-k2", DisplayName: "Kimi K2", ContextWindow: 262144,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
		},
	},
	{
		ID: "moonshot-cn", DisplayName: "Kimi (China)", Family: "moonshot", RegionTag: "CN", BrandColor: "#7C3AED",
		Description: "Moonshot / Kimi 中国区（OpenAI 兼容）。",
		BaseURL:     "https://api.moonshot.cn/v1", QuotaKind: "moonshot",
		Kind: "vendor", Icon: "kimi",
		Website: "https://platform.kimi.com", KeysURL: "https://platform.kimi.com/console/api-keys",
		Models: []ir.ModelMeta{
			{ID: "moonshot-cn/kimi-k2", UpstreamModel: "kimi-k2", DisplayName: "Kimi K2", ContextWindow: 262144,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
		},
	},
	{
		ID: "zhipu", DisplayName: "Zhipu GLM", Family: "zhipu", RegionTag: "CN", BrandColor: "#3859FF",
		Description: "智谱 GLM 开放平台（OpenAI 兼容）。",
		BaseURL:     "https://open.bigmodel.cn/api/paas/v4",
		Kind:        "vendor", Icon: "zhipu-color",
		Website: "https://open.bigmodel.cn", KeysURL: "https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys",
		RegionLabel: "Plan", Regions: []Region{
			{ID: "api", Name: "Pay as you go", Chat: "https://open.bigmodel.cn/api/paas/v4", Anthropic: "https://open.bigmodel.cn/api/anthropic"},
			{ID: "coding", Name: "Coding Plan", Chat: "https://open.bigmodel.cn/api/coding/paas/v4", Responses: "https://open.bigmodel.cn/api/v1", Anthropic: "https://open.bigmodel.cn/api/anthropic"},
		},
		Models: []ir.ModelMeta{
			{ID: "zhipu/glm-5.3", UpstreamModel: "glm-5.3", DisplayName: "GLM-5.3", ContextWindow: 200000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true}},
			{ID: "zhipu/glm-5.2", UpstreamModel: "glm-5.2", DisplayName: "GLM-5.2", ContextWindow: 200000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true}},
		},
	},
	{
		ID: "minimax", DisplayName: "MiniMax", Family: "minimax", RegionTag: "GLOBAL", BrandColor: "#FF5C00",
		Description: "MiniMax 开放平台 chat completions（OpenAI 兼容）；订阅额度仍走 MiniMax Code 渠道。",
		BaseURL:     "https://api.minimax.io/v1",
		Kind:        "vendor", Icon: "minimax-color",
		Website: "https://platform.minimax.io", KeysURL: "https://platform.minimax.io/user-center/basic-information/interface-key",
		Models: []ir.ModelMeta{
			{ID: "minimax/MiniMax-M2", UpstreamModel: "MiniMax-M2", DisplayName: "MiniMax M2", ContextWindow: 204800,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true}},
		},
	},
	{
		ID: "groq", DisplayName: "Groq", Family: "groq", RegionTag: "GLOBAL", BrandColor: "#F55036",
		Description: "Groq 推理云（OpenAI 兼容）。",
		BaseURL:     "https://api.groq.com/openai/v1",
		Kind:        "vendor", Icon: "groq",
		Website: "https://console.groq.com", KeysURL: "https://console.groq.com/keys",
		Models: []ir.ModelMeta{
			{ID: "groq/llama-3.3-70b-versatile", UpstreamModel: "llama-3.3-70b-versatile", DisplayName: "Llama 3.3 70B", ContextWindow: 131072,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
			{ID: "groq/openai-gpt-oss-120b", UpstreamModel: "openai/gpt-oss-120b", DisplayName: "GPT-OSS 120B", ContextWindow: 131072,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true}},
		},
	},
	{
		ID: "mistral", DisplayName: "Mistral", Family: "mistral", RegionTag: "GLOBAL", BrandColor: "#FA520F",
		Description: "Mistral AI 平台（OpenAI 兼容）。",
		BaseURL:     "https://api.mistral.ai/v1",
		Kind:        "vendor", Icon: "mistral-color",
		Website: "https://console.mistral.ai", KeysURL: "https://console.mistral.ai/api-keys",
		Models: []ir.ModelMeta{
			{ID: "mistral/mistral-large-latest", UpstreamModel: "mistral-large-latest", DisplayName: "Mistral Large", ContextWindow: 131072,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
		},
	},
	{
		ID: "cline", DisplayName: "Cline", Family: "cline", RegionTag: "GLOBAL", BrandColor: "#22C55E",
		Description: "Cline API（OpenAI 兼容，多模型聚合）；余额与 ClinePass 5h/7d/30d 窗口可读。",
		BaseURL:     "https://api.cline.bot/api/v1", QuotaKind: "cline",
		Kind: "vendor", Icon: "cline",
		Website: "https://cline.bot", KeysURL: "https://app.cline.bot/settings/api-keys",
	},
	{
		ID: "zai", DisplayName: "Z.ai", Family: "zhipu", RegionTag: "GLOBAL", BrandColor: "#3859FF",
		Description: "Z.ai（智谱国际）OpenAI 兼容端点。",
		BaseURL:     "https://api.z.ai/api/paas/v4",
		Kind:        "vendor", Icon: "zai",
		Website: "https://z.ai", KeysURL: "https://z.ai/manage-apikey/apikey-list",
		RegionLabel: "Plan", Regions: []Region{
			{ID: "api", Name: "Pay as you go", Chat: "https://api.z.ai/api/paas/v4", Anthropic: "https://api.z.ai/api/anthropic"},
			{ID: "coding", Name: "Coding Plan", Chat: "https://api.z.ai/api/coding/paas/v4", Responses: "https://api.z.ai/api/v1", Anthropic: "https://api.z.ai/api/anthropic"},
		},
	},
	{
		ID: "volcengine", DisplayName: "Volcengine Ark", Family: "volcengine", RegionTag: "CN", BrandColor: "#1664FF",
		Description: "火山方舟：Coding Plan / Agent Plan / 按量计费端点分开（OpenAI 兼容；另有 /messages）。",
		BaseURL:     "https://ark.cn-beijing.volces.com/api/coding/v3",
		Kind:        "vendor", Icon: "volcengine-color", Note: "火山方舟 · Coding / Agent Plan",
		Website: "https://www.volcengine.com/docs/82379/1925114", KeysURL: "https://ark.volcengine.com/region:cn-beijing/apikey",
		RegionLabel: "Plan", Regions: []Region{
			{ID: "coding", Name: "Coding Plan", Chat: "https://ark.cn-beijing.volces.com/api/coding/v3", Responses: "https://ark.cn-beijing.volces.com/api/coding/v3", Anthropic: "https://ark.cn-beijing.volces.com/api/coding"},
			{ID: "agent", Name: "Agent Plan", Chat: "https://ark.cn-beijing.volces.com/api/plan/v3", Responses: "https://ark.cn-beijing.volces.com/api/plan/v3", Anthropic: "https://ark.cn-beijing.volces.com/api/plan"},
			{ID: "api", Name: "Pay as you go", Chat: "https://ark.cn-beijing.volces.com/api/v3", Responses: "https://ark.cn-beijing.volces.com/api/v3"},
		},
	},
	{
		ID: "tencent-cloud", DisplayName: "Tencent Cloud", Family: "tencent", RegionTag: "CN", BrandColor: "#0052D9",
		Description: "腾讯云 TokenHub：Token Plan / 按量计费（中国 / 国际）/ 企业版端点分开。",
		BaseURL:     "https://api.lkeap.cloud.tencent.com/plan/v3",
		Kind:        "vendor", Icon: "tencentcloud-color", Note: "Token Plan · pay as you go",
		Website: "https://cloud.tencent.com/document/product/1823/130060", KeysURL: "https://console.cloud.tencent.com/tokenhub/tokenplan",
		RegionLabel: "Plan", Regions: []Region{
			{ID: "plan", Name: "Plan · China", Chat: "https://api.lkeap.cloud.tencent.com/plan/v3", Anthropic: "https://api.lkeap.cloud.tencent.com/plan/anthropic", KeysURL: "https://console.cloud.tencent.com/tokenhub/tokenplan"},
			{ID: "cn", Name: "Pay as you go · China", Chat: "https://tokenhub.tencentmaas.com/v1", Responses: "https://tokenhub.tencentmaas.com/v1", Anthropic: "https://tokenhub.tencentmaas.com", KeysURL: "https://console.cloud.tencent.com/tokenhub/apikey"},
			{ID: "intl", Name: "Pay as you go · Global", Chat: "https://tokenhub-intl.tencentmaas.com/v1", Responses: "https://tokenhub-intl.tencentmaas.com/v1", Anthropic: "https://tokenhub-intl.tencentmaas.com", KeysURL: "https://console.tencentcloud.com/tokenhub/apikey"},
			{ID: "enterprise", Name: "Enterprise plan · China", Chat: "https://tokenhub.tencentmaas.com/plan/v3", Responses: "https://tokenhub.tencentmaas.com/plan/v3", Anthropic: "https://tokenhub.tencentmaas.com/plan/anthropic", KeysURL: "https://console.cloud.tencent.com/tokenhub/tokenplan"},
			{ID: "enterprise-intl", Name: "Enterprise plan · Global", Chat: "https://tokenhub-intl.tencentmaas.com/plan/v3", Responses: "https://tokenhub-intl.tencentmaas.com/plan/v3", Anthropic: "https://tokenhub-intl.tencentmaas.com/plan/anthropic", KeysURL: "https://console.tencentcloud.com/tokenhub/tokenplan"},
		},
	},
	{
		ID: "xiaomi", DisplayName: "Xiaomi MiMo", Family: "xiaomi", RegionTag: "CN", BrandColor: "#FF6900",
		Description: "小米 MiMo Token Plan（中国/新加坡/欧洲）与按量计费端点分开。",
		BaseURL:     "https://token-plan-cn.xiaomimimo.com/v1",
		Kind:        "vendor", Icon: "mimocode", Note: "Token Plan · pay as you go",
		Website: "https://platform.xiaomimimo.com", KeysURL: "https://platform.xiaomimimo.com/token-plan",
		RegionLabel: "Plan", Regions: []Region{
			{ID: "plan-cn", Name: "Plan · China", Chat: "https://token-plan-cn.xiaomimimo.com/v1", Responses: "https://token-plan-cn.xiaomimimo.com/v1", Anthropic: "https://token-plan-cn.xiaomimimo.com/anthropic"},
			{ID: "plan-sgp", Name: "Plan · Singapore", Chat: "https://token-plan-sgp.xiaomimimo.com/v1", Responses: "https://token-plan-sgp.xiaomimimo.com/v1", Anthropic: "https://token-plan-sgp.xiaomimimo.com/anthropic"},
			{ID: "plan-ams", Name: "Plan · Europe", Chat: "https://token-plan-ams.xiaomimimo.com/v1", Responses: "https://token-plan-ams.xiaomimimo.com/v1", Anthropic: "https://token-plan-ams.xiaomimimo.com/anthropic"},
			{ID: "api", Name: "Pay as you go", Chat: "https://api.xiaomimimo.com/v1", Responses: "https://api.xiaomimimo.com/v1", Anthropic: "https://api.xiaomimimo.com/anthropic"},
		},
	},
	{
		ID: "baidu-qianfan", DisplayName: "Baidu Qianfan", Family: "qianfan", RegionTag: "CN", BrandColor: "#2932E1",
		Description: "百度千帆 Token Plan（个人 / 企业）与按量计费 v2 端点分开。",
		BaseURL:     "https://qianfan.baidubce.com/v2/tokenplan/personal",
		Kind:        "vendor", Icon: "baiducloud-color", Note: "Token Plan · pay as you go",
		Website: "https://cloud.baidu.com/doc/qianfan/s/Dmrabu8b6", KeysURL: "https://console.bce.baidu.com/qianfan/resource/token-plan",
		RegionLabel: "Plan", Regions: []Region{
			{ID: "personal", Name: "Token Plan Personal", Chat: "https://qianfan.baidubce.com/v2/tokenplan/personal", Responses: "https://qianfan.baidubce.com/v2/tokenplan/personal", Anthropic: "https://qianfan.baidubce.com/anthropic/tokenplan/personal"},
			{ID: "team", Name: "Token Plan Enterprise", Chat: "https://qianfan.baidubce.com/v2/tokenplan/team", Responses: "https://qianfan.baidubce.com/v2/tokenplan/team", Anthropic: "https://qianfan.baidubce.com/anthropic/tokenplan/team"},
			{ID: "api", Name: "Pay as you go", Chat: "https://qianfan.baidubce.com/v2", Responses: "https://qianfan.baidubce.com/v2", Anthropic: "https://qianfan.baidubce.com/anthropic", KeysURL: "https://console.bce.baidu.com/iam/#/iam/apikey/list"},
		},
	},
	{
		ID: "bedrock", DisplayName: "Amazon Bedrock", Family: "bedrock", RegionTag: "GLOBAL", BrandColor: "#FF9900",
		Description: "Amazon Bedrock（Bedrock API Key 或 AWS IAM SigV4）。OpenAI 兼容端点 /openai/v1 提供 chat 与 Responses；Claude 模型自动走原生 invoke（thinking / 工具 / 缓存完整）。",
		BaseURL:     "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1",
		Kind:        "vendor", Icon: "bedrock-color", Note: "Bedrock API key",
		Website: "https://aws.amazon.com/bedrock/", KeysURL: "https://console.aws.amazon.com/bedrock/home#/api-keys",
		RegionLabel: "Region", Regions: []Region{
			{ID: "us-east-1", Name: "US East (N. Virginia)", Chat: "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1", Responses: "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1"},
			{ID: "us-west-2", Name: "US West (Oregon)", Chat: "https://bedrock-runtime.us-west-2.amazonaws.com/openai/v1", Responses: "https://bedrock-runtime.us-west-2.amazonaws.com/openai/v1"},
			{ID: "eu-central-1", Name: "Europe (Frankfurt)", Chat: "https://bedrock-runtime.eu-central-1.amazonaws.com/openai/v1", Responses: "https://bedrock-runtime.eu-central-1.amazonaws.com/openai/v1"},
			{ID: "eu-west-1", Name: "Europe (Ireland)", Chat: "https://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1", Responses: "https://bedrock-runtime.eu-west-1.amazonaws.com/openai/v1"},
			{ID: "ap-northeast-1", Name: "Asia Pacific (Tokyo)", Chat: "https://bedrock-runtime.ap-northeast-1.amazonaws.com/openai/v1", Responses: "https://bedrock-runtime.ap-northeast-1.amazonaws.com/openai/v1"},
			{ID: "ap-southeast-1", Name: "Asia Pacific (Singapore)", Chat: "https://bedrock-runtime.ap-southeast-1.amazonaws.com/openai/v1", Responses: "https://bedrock-runtime.ap-southeast-1.amazonaws.com/openai/v1"},
		},
		Models: []ir.ModelMeta{
			{ID: "bedrock/deepseek.v3.2", UpstreamModel: "deepseek.v3.2", DisplayName: "DeepSeek v3.2 (Bedrock)", ContextWindow: 128000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
			{ID: "bedrock/openai.gpt-oss-120b-1:0", UpstreamModel: "openai.gpt-oss-120b-1:0", DisplayName: "GPT-OSS 120B (Bedrock)", ContextWindow: 131072,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true}},
			{ID: "bedrock/qwen.qwen3-coder-480b-a35b-v1:0", UpstreamModel: "qwen.qwen3-coder-480b-a35b-v1:0", DisplayName: "Qwen3 Coder 480B (Bedrock)", ContextWindow: 262144,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
		},
	},
	{
		ID: "minimax-cn", DisplayName: "MiniMax (China)", Family: "minimax", RegionTag: "CN", BrandColor: "#FF5C00",
		Description: "MiniMax 中国站 chat completions（OpenAI 兼容）。",
		BaseURL:     "https://api.minimaxi.com/v1",
		Kind:        "vendor", Icon: "minimax-color",
		Website: "https://platform.minimaxi.com", KeysURL: "https://platform.minimaxi.com/user-center/basic-information/interface-key",
	},
	{
		ID: "qwen-token-plan", DisplayName: "Qwen Token Plan", Family: "qwen", RegionTag: "GLOBAL", BrandColor: "#615CED",
		Description: "Qwen Token Plan（订阅制，compatible-mode 端点）。",
		BaseURL:     "https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1",
		Kind:        "vendor", Icon: "qwen-color", Note: "Qwen AI platform · subscription",
		Website: "https://platform.qianwenai.com/docs/token-plan/overview", KeysURL: "https://platform.qianwenai.com/home/analytics/token-plan/individual",
	},
	{
		ID: "bailian-token-plan", DisplayName: "Bailian Token Plan", Family: "qwen", RegionTag: "CN", BrandColor: "#615CED",
		Description: "阿里云百炼 Token Plan（订阅制，compatible-mode 端点）。",
		BaseURL:     "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
		Kind:        "vendor", Icon: "bailian-color", Note: "Alibaba Cloud Bailian · subscription",
		Website: "https://help.aliyun.com/zh/model-studio/token-plan-overview", KeysURL: "https://bailian.console.aliyun.com/cn-beijing/subscription/token-plan/personal",
	},
	{
		ID: "stepfun", DisplayName: "StepFun", Family: "stepfun", RegionTag: "GLOBAL", BrandColor: "#0EA5E9",
		Description: "阶跃星辰 StepFun（OpenAI 兼容）；Step Plan 与按量计费端点分开。",
		BaseURL:     "https://api.stepfun.ai/step_plan/v1",
		Kind:        "vendor", Icon: "stepfun-color",
		Website: "https://platform.stepfun.ai", KeysURL: "https://platform.stepfun.ai/interface-key",
		RegionLabel: "Plan", Regions: []Region{
			{ID: "plan", Name: "Step Plan", Chat: "https://api.stepfun.ai/step_plan/v1", Anthropic: "https://api.stepfun.ai/step_plan"},
			{ID: "api", Name: "Pay as you go", Chat: "https://api.stepfun.ai/v1", Anthropic: "https://api.stepfun.ai"},
		},
	},
	{
		ID: "qwen", DisplayName: "Qwen", Family: "qwen", RegionTag: "GLOBAL", BrandColor: "#615CED",
		Description: "阿里云百炼 DashScope 国际站（OpenAI 兼容）。",
		BaseURL:     "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
		Kind:        "vendor", Icon: "qwen-color",
		Website: "https://modelstudio.console.alibabacloud.com", KeysURL: "https://bailian.console.aliyun.com",
	},
	{
		ID: "qwen-cn", DisplayName: "Qwen (China)", Family: "qwen", RegionTag: "CN", BrandColor: "#615CED",
		Description: "阿里云百炼 DashScope 中国站（OpenAI 兼容）。",
		BaseURL:     "https://dashscope.aliyuncs.com/compatible-mode/v1",
		Kind:        "vendor", Icon: "qwen-color",
		Website: "https://bailian.console.aliyun.com", KeysURL: "https://bailian.console.aliyun.com/?tab=model#/api-key",
	},
	{
		ID: "kimi-code", DisplayName: "Kimi Code", Family: "moonshot", RegionTag: "GLOBAL", BrandColor: "#7C3AED",
		Description: "Kimi Code 会员端点（OpenAI 兼容，会员 Key）。",
		BaseURL:     "https://api.kimi.ai/coding/v1",
		Kind:        "vendor", Icon: "kimi", Note: "Membership",
		Website: "https://www.kimi.ai/code", KeysURL: "https://www.kimi.ai/code/console",
	},
	{
		ID: "siliconflow", DisplayName: "SiliconFlow", Family: "siliconflow", RegionTag: "CN", BrandColor: "#0EA5E9",
		Description: "硅基流动 SiliconFlow（OpenAI 兼容）。",
		BaseURL:     "https://api.siliconflow.cn/v1", QuotaKind: "siliconflow",
		Kind: "relay", Icon: "siliconflow",
		Website: "https://cloud.siliconflow.cn", KeysURL: "https://cloud.siliconflow.cn/account/ak",
	},
	{
		ID: "ollama-cloud", DisplayName: "Ollama Cloud", Family: "ollama", RegionTag: "GLOBAL", BrandColor: "#111827",
		Description: "Ollama 云端推理（OpenAI 兼容，需要 API Key）。",
		BaseURL:     "https://ollama.com/v1",
		Kind:        "vendor", Icon: "ollama",
		Website: "https://docs.ollama.com/cloud", KeysURL: "https://ollama.com/settings/keys",
	},
	{
		ID: "commandcode", DisplayName: "Command Code", Family: "commandcode", RegionTag: "GLOBAL", BrandColor: "#7C3AED",
		Description: "Command Code Provider API（OpenAI 兼容 chat；Claude 模型只提供 /messages）；积分与滚动窗口可读。",
		BaseURL:     "https://api.commandcode.ai/provider/v1", QuotaKind: "commandcode",
		Kind: "vendor", Icon: "commandcode",
		Website: "https://commandcode.ai", KeysURL: "https://commandcode.ai/dashboard",
		Models: []ir.ModelMeta{
			{ID: "commandcode/deepseek-deepseek-v4-flash", UpstreamModel: "deepseek/deepseek-v4-flash", DisplayName: "DeepSeek V4 Flash", ContextWindow: 128000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
		},
	},
	{
		ID: "openrouter", DisplayName: "OpenRouter", Family: "openrouter", RegionTag: "GLOBAL", BrandColor: "#6366F1",
		Description: "OpenRouter 聚合端点：一个 Key 调多家模型（OpenAI 兼容）。",
		BaseURL:     "https://openrouter.ai/api/v1", QuotaKind: "openrouter",
		Kind: "relay", Icon: "openrouter",
		Website: "https://openrouter.ai", KeysURL: "https://openrouter.ai/keys", Note: "one key, many vendors",
		Models: []ir.ModelMeta{
			{ID: "openrouter/openai-gpt-4o", UpstreamModel: "openai/gpt-4o", DisplayName: "OpenRouter · GPT-4o", ContextWindow: 128000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Vision: true, Streaming: true}},
			{ID: "openrouter/anthropic-claude-sonnet", UpstreamModel: "anthropic/claude-sonnet-4.5", DisplayName: "OpenRouter · Claude Sonnet", ContextWindow: 200000,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Vision: true, Streaming: true}},
		},
	},
	{
		ID: "ollama", DisplayName: "Ollama", Family: "ollama", RegionTag: "LOCAL", BrandColor: "#111827",
		Description: "本机 Ollama（OpenAI 兼容端点，通常不需要 Key）。",
		BaseURL:     "http://127.0.0.1:11434/v1", NoKey: true,
		Kind: "local", Icon: "ollama",
		Website: "https://ollama.com",
		Models: []ir.ModelMeta{
			{ID: "ollama/llama3.2", UpstreamModel: "llama3.2", DisplayName: "Llama 3.2 (local)", ContextWindow: 131072,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
			{ID: "ollama/qwen3", UpstreamModel: "qwen3", DisplayName: "Qwen3 (local)", ContextWindow: 131072,
				Capabilities: ir.ModelCapabilities{ToolCall: true, Streaming: true}},
		},
	},
	{
		ID: "lmstudio", DisplayName: "LM Studio", Family: "lmstudio", RegionTag: "LOCAL", BrandColor: "#0EA5E9",
		Description: "本机 LM Studio 服务端（OpenAI 兼容，通常不需要 Key）。",
		BaseURL:     "http://127.0.0.1:1234/v1", NoKey: true,
		Kind: "local", Icon: "lmstudio",
		Website: "https://lmstudio.ai",
	},
	{
		ID: "vllm", DisplayName: "vLLM", Family: "vllm", RegionTag: "LOCAL", BrandColor: "#0EA5E9",
		Description: "自建 vLLM / llama.cpp server（OpenAI 兼容，通常不需要 Key）。",
		BaseURL:     "http://127.0.0.1:8000/v1", NoKey: true,
		Kind: "local", Icon: "vllm",
		Website: "https://docs.vllm.ai",
	},
}

// Vendors 返回全部一等公民厂商（副本）。
func Vendors() []Vendor {
	out := make([]Vendor, len(vendors))
	copy(out, vendors)
	return out
}

// PresetInfo 是「添加供应商」弹层的一条预设（aether PresetDef 的子集）。
type PresetInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Icon        string   `json:"icon"`
	Kind        string   `json:"kind"`
	Chat        string   `json:"chat,omitempty"`
	Website     string   `json:"website,omitempty"`
	KeysURL     string   `json:"keysUrl,omitempty"`
	Note        string   `json:"note,omitempty"`
	Short       string   `json:"short,omitempty"`
	NoKey       bool     `json:"noKey,omitempty"`
	Regions     []Region `json:"regions,omitempty"`
	RegionLabel string   `json:"regionLabel,omitempty"`
}

// Presets 返回按厂商顺序的预设。
func Presets() []PresetInfo {
	out := make([]PresetInfo, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, PresetInfo{
			ID: v.ID, Name: v.DisplayName, Icon: v.Icon, Kind: v.Kind,
			Chat: v.BaseURL, Website: v.Website, KeysURL: v.KeysURL,
			Note: v.Note, Short: v.Short, NoKey: v.NoKey,
			Regions: v.Regions, RegionLabel: v.RegionLabel,
		})
	}
	return out
}
