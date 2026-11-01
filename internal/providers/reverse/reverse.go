// Package reverse 是逆向渠道的通用引擎骨架。
//
// 目标渠道（WorkBuddy / Trae / Qoder / Antigravity / Kiro / Codex / OpenCode）
// 在协议细节上各不相同，但它们共享同一套"机制"：
//
//   - 渠道参数（base_url、端点、版本号、头模板、签名盐值、模型映射）来自可热更新的 spec；
//   - 账号凭证与设备指纹由内核统一管理，网络出口按 RegionProfile 分流；
//   - 上游流式响应统一解码成 IR 的 4 种极简事件，协议差异由 spec 里的字段映射描述。
//
// 本包负责的就是这套机制。**协议细节（抓包得到的真实字段名与端点）不写死在代码里**，
// 而是留给 configs/specs/<provider>.json；spec 未配置时请求会返回明确错误，
// 不会静默走错协议。
//
// 这样做的收益：拿到抓包数据后，通常只需要改 JSON（必要时加一个签名算法），
// 不需要为每个渠道复制一份几百行的 provider 实现。
package reverse

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// Profile 描述一个逆向渠道的静态差异（同一产品的 CN / Intl 变体共用引擎）。
type Profile struct {
	ID          string
	DisplayName string
	Family      string
	RegionTag   string
	BrandColor  string
	Description string

	// NetworkPolicy 决定账号级 HTTP 客户端走直连还是代理。
	NetworkPolicy provider.NetworkPolicy

	// Auths 静态支持的登录方式；spec 或 OAuth 声明后会自动追加 PKCE / 设备码。
	Auths []provider.AuthMethod

	// OAuth 是代码内置的 OAuth 参数（spec 可覆盖），用于端点稳定的订阅渠道。
	OAuth *provider.OAuthConfig

	// Middlewares 默认挂载的补偿中间件；spec 可用 middlewares 字段覆盖。
	Middlewares []string

	// DefaultBaseURL 是账号未提供 base_url 且 spec 未声明 base_url 时的兜底上游地址。
	// 只对"端点已知且稳定"的渠道填写；留空表示必须由 spec 或账号提供。
	DefaultBaseURL string

	// DefaultModels 是渠道自带的标准模型清单（上游模型名 == 对外模型名）。
	// 用于 /v1/models 展示；未登记的模型仍可通过 "<id>/<模型名>" 前缀透传。
	DefaultModels []string

	// Headers 是代码内置的固定请求头（spec.HeadersTemplate 可覆盖同名头）。
	Headers map[string]string

	// SessionHeader 要求把本请求的会话标识写入该请求头（例如 OpenCode Go
	// 的 x-opencode-session）。值取网关已算好的 req.SessionKey，跨轮稳定。
	SessionHeader string

	// ModelListPath 是上游模型列表端点（如 "/models"）；空表示该渠道
	// 没有可用的模型列表接口，实时目录不会尝试刷新它。
	ModelListPath string

	// DefaultChatPath / DefaultResponsesPath 是 spec 未声明 endpoints 时的兜底路径。
	// 留空表示"必须由 spec 提供"，避免猜错端点。
	DefaultChatPath      string
	DefaultResponsesPath string

	// ResponsesWSEndpoint 让该渠道通过上游 Responses WebSocket 直通
	// （wss/ws 绝对地址，或相对账号 base_url 的路径）。空表示不支持；
	// spec 的 endpoints.responses_ws 可覆盖。Codex 官方订阅走内置默认，不依赖此字段。
	ResponsesWSEndpoint string

	// NativeResponses 表示上游原生支持 OpenAI Responses，可走 Fast-Path。
	NativeResponses bool
	// NativeResponsesNonStream 允许非流式 Responses 也走直通。
	NativeResponsesNonStream bool

	// AutoModel 表示该渠道提供一个 "<id>/auto" 模型，
	// 上游真实模型名从账号的 model 凭证读取（典型来源：本机导入客户端配置）。
	AutoModel bool

	// Refresh 表示渠道有自定义的 token 刷新实现（目前是 Codex 订阅）。
	Refresh bool

	// OfficialFirst 声明"官方订阅账号优先"：选号时先官方订阅、再第三方中转。
	OfficialFirst bool

	// OfficialAccount 判断一份凭证是不是官方订阅登录态；配合 OfficialFirst 使用。
	OfficialAccount func(credentials map[string]string) bool

	// OfficialBinding 解析官方绑定信息（账号/邮箱/套餐）；nil 表示不支持。
	OfficialBinding func(credentials map[string]string) *provider.OfficialBinding

	// QuotaKind 声明用量读取方式：
	//   - "relay"：relay-panel/relay-panel 一类中继面板的账单接口（剩余美元）；
	//   - "opencode_go"：OpenCode Go 订阅的滚动窗口。
	// 留空表示该渠道没有已知的用量接口，用量页不会为它发起读取。
	QuotaKind string
}

// autoModelUpstream 是 "<provider>/auto" 在路由层的内部标记。
const autoModelUpstream = "__auto__"

// Provider 是逆向渠道的通用实现。
type Provider struct {
	profile Profile

	// Manifest 会在每次路由重建时被调用；spec 热更新后需要重新生成。
	// 缓存键用 spec_version + 来源，避免同一秒内两次热更新读到旧 Manifest。
	mu        sync.Mutex
	cachedKey string
	cached    provider.ProviderManifest
}

// New 创建一个逆向渠道实例。
func New(p Profile) *Provider {
	return &Provider{profile: p}
}

// Profile 返回静态配置（测试与诊断用）。
func (p *Provider) Profile() Profile { return p.profile }

// IsOfficialAccount 实现 provider.OfficialAccountCapability。
func (p *Provider) IsOfficialAccount(credentials map[string]string) bool {
	if p.profile.OfficialAccount == nil {
		return false
	}
	return p.profile.OfficialAccount(credentials)
}

// OfficialBinding 实现 provider.OfficialBindingCapability。
func (p *Provider) OfficialBinding(credentials map[string]string) *provider.OfficialBinding {
	if p.profile.OfficialBinding == nil {
		return nil
	}
	return p.profile.OfficialBinding(credentials)
}

// Manifest 生成渠道自描述。
//
// 它会合并 spec 里的可变部分：
//   - OAuth 参数 -> 自动追加 oauth_pkce / device_code 登录方式；
//   - middlewares -> 覆盖默认补偿中间件；
//   - models -> 生成对外模型清单（键为对外名，值为上游名）。
func (p *Provider) Manifest() provider.ProviderManifest {
	sp := spec.Load(p.profile.ID)

	var key string
	if sp != nil {
		key = strconv.FormatInt(sp.Revision, 10)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached.ID != "" && p.cachedKey == key {
		return p.cached
	}

	p.cached = p.buildManifest(sp)
	p.cachedKey = key
	return p.cached
}

func (p *Provider) buildManifest(sp *spec.Spec) provider.ProviderManifest {
	m := provider.ProviderManifest{
		ID:                  p.profile.ID,
		DisplayName:         p.profile.DisplayName,
		Family:              p.profile.Family,
		RegionTag:           p.profile.RegionTag,
		BrandColor:          p.profile.BrandColor,
		Description:         p.profile.Description,
		NetworkPolicy:       p.profile.NetworkPolicy,
		SupportedAuths:      append([]provider.AuthMethod(nil), p.profile.Auths...),
		AuthFormSchema:      p.authForm(),
		RequiredMiddlewares: append([]string(nil), p.profile.Middlewares...),
		NativeResponses:     p.profile.NativeResponses,
		Capabilities: provider.CapabilitySet{
			Chat:                          true,
			LocalImport:                   true,
			OfficialFirst:                 p.profile.OfficialFirst,
			OfficialBinding:               p.profile.OfficialBinding != nil,
			Refresh:                       p.profile.Refresh,
			Quota:                         p.profile.QuotaKind != "",
			ResponsivePassthrough:         p.profile.NativeResponses,
			ResponsesPassthroughNonStream: p.profile.NativeResponsesNonStream,
		},
	}

	// 自动模型：真实模型名来自账号凭证（本机导入的客户端配置）。
	// 它不依赖 spec，所以必须在 sp==nil 的早退之前追加。
	if p.profile.AutoModel {
		m.Models = append(m.Models, ir.ModelMeta{
			ID:            p.profile.ID + "/auto",
			UpstreamModel: autoModelUpstream,
			DisplayName:   "自动（读取本机配置）",
			Capabilities:  ir.ModelCapabilities{Streaming: true, ToolCall: true, Reasoning: true},
		})
	}

	for _, name := range p.profile.DefaultModels {
		m.Models = append(m.Models, ir.ModelMeta{
			ID:            p.profile.ID + "/" + name,
			UpstreamModel: name,
			DisplayName:   name,
			Capabilities:  ir.ModelCapabilities{Streaming: true, ToolCall: true, Reasoning: true},
		})
	}

	if p.profile.OAuth != nil {
		m.OAuth = p.profile.OAuth
		m.SupportedAuths = appendAuth(m.SupportedAuths, provider.AuthOAuthPKCE)
		if p.profile.OAuth.TokenURL != "" {
			m.Capabilities.Refresh = true
		}
	}

	if sp == nil {
		return m
	}

	// 中间件可以按抓包结果覆盖：例如上游不支持原生 tools 时加 xml_tool_call。
	if len(sp.Middlewares) > 0 {
		m.RequiredMiddlewares = append([]string(nil), sp.Middlewares...)
	}

	// OAuth：spec 一填，登录方式与刷新能力就自动出现，前端无需改代码。
	if sp.OAuth != nil {
		m.OAuth = oauthFromSpec(sp.OAuth)
		m.SupportedAuths = appendAuth(m.SupportedAuths, provider.AuthOAuthPKCE)
		if sp.OAuth.DeviceCodeURL != "" {
			m.SupportedAuths = appendAuth(m.SupportedAuths, provider.AuthDeviceCode)
		}
		if sp.OAuth.TokenURL != "" {
			m.Capabilities.Refresh = true
		}
	}

	// spec.Models 既用于请求映射，也作为对外模型清单。
	for gatewayName, upstream := range sp.Models {
		if gatewayName == "" || upstream == "" {
			continue
		}
		id := gatewayName
		if !strings.Contains(id, "/") {
			id = p.profile.ID + "/" + id
		}
		m.Models = append(m.Models, ir.ModelMeta{
			ID:            id,
			UpstreamModel: upstream,
			DisplayName:   upstream,
			Capabilities:  ir.ModelCapabilities{Streaming: true},
		})
	}
	sort.Slice(m.Models, func(i, j int) bool { return m.Models[i].ID < m.Models[j].ID })

	return m
}

func appendAuth(list []provider.AuthMethod, add provider.AuthMethod) []provider.AuthMethod {
	for _, a := range list {
		if a == add {
			return list
		}
	}
	return append(list, add)
}

// authForm 是逆向渠道的手动凭证表单；Codex 额外收 ChatGPT 订阅的账号 ID，
// 用来读 /wham/usage 的 5h/7d 额度（中转账号留空即可）。
func (p *Provider) authForm() []provider.FormFieldSchema {
	// OpenCode 两个渠道都用 API Key（没有 OAuth/access_token），
	// 表单别再让用户把 Key 填进「Access Token」。
	if p.profile.ID == "opencode" || p.profile.ID == "opencode-go" {
		return []provider.FormFieldSchema{
			{
				Key:         "base_url",
				Label:       "Base URL（可选）",
				Type:        "text",
				Placeholder: p.profile.DefaultBaseURL,
				Help:        "留空使用渠道默认端点",
			},
			{
				Key:         "api_key",
				Label:       "API Key",
				Type:        "password",
				Placeholder: "在 https://opencode.ai/auth 创建并复制",
				Required:    true,
			},
		}
	}
	form := manualAuthForm()
	if p.profile.ID == "codex" {
		form = append(form, provider.FormFieldSchema{
			Key: "account_id", Label: "ChatGPT Account ID（订阅账号）", Type: "text",
			Help: "ChatGPT 订阅账号填写后，用量页读 Codex 的 5h/7d 额度；中转只填 Base URL + Token 即可",
		})
	}
	return form
}

// manualAuthForm 是逆向渠道最常见的手动凭证表单。
//
// base_url 允许留空：spec 配好后由 spec 统一提供；未配 spec 时必须手填。
func manualAuthForm() []provider.FormFieldSchema {
	return []provider.FormFieldSchema{
		{
			Key:         "base_url",
			Label:       "Base URL（可选）",
			Type:        "text",
			Placeholder: "https://example.com",
			Help:        "spec 未配置 base_url 时必填；末尾不要带斜杠",
		},
		{
			Key:         "access_token",
			Label:       "Access Token / Cookie",
			Type:        "textarea",
			Placeholder: "从客户端配置或抓包中得到的凭证",
			Required:    true,
		},
		{
			Key:   "refresh_token",
			Label: "Refresh Token（可选）",
			Type:  "password",
			Help:  "有刷新端点时填写，令牌过期后可自动续期",
		},
	}
}

// 编译期确认实现的能力。
var (
	_ provider.BaseProvider              = (*Provider)(nil)
	_ provider.ChatCapability            = (*Provider)(nil)
	_ provider.LocalImportCapability     = (*Provider)(nil)
	_ provider.NativeResponsesCapability = (*Provider)(nil)
)
