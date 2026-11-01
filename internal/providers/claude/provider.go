// Package claude 实现"Claude 订阅"渠道：把本机 Claude Code CLI 已登录的
// Pro/Max 订阅（OAuth 凭证）变成网关可用的上游账号，让任何下游客户端都能
// 复用这份订阅算力（对标 magpie 的订阅共享玩法）。
//
// 协议要点（订阅 OAuth token 与普通 API key 的差别）：
//   - 对话：POST {base}/v1/messages，标准 Anthropic Messages 协议。鉴权不用
//     x-api-key，而是 Authorization: Bearer <oauth access_token>，且必须携带
//     anthropic-beta: oauth-2025-04-20 —— 不带该 beta 头，订阅 token 会被
//     API 直接拒绝（见 chat.go 中头值来源注释）；
//   - 刷新：POST https://console.anthropic.com/v1/oauth/token，JSON 体
//     {grant_type: refresh_token, refresh_token, client_id}，client_id 用
//     Claude Code 的公开客户端 ID（见 auth.go 注释）；
//   - 响应：stream=true 时是标准 SSE（message_start / content_block_* /
//     message_delta / message_stop），stream=false 时是单个 JSON 对象。
//
// 与其它渠道一致，本包只产出 ir.UnifiedStreamEvent 的极简事件；
// Anthropic 特有的 content_block 生命周期（start/delta/stop、按 index 分块）
// 全部在包内消化，不给内核添协议负担。
package claude

import (
	"sync"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func init() {
	provider.Register(&Provider{})
}

// channelID 是渠道在注册表与模型前缀里的唯一标识。
const channelID = "claude-sub"

// Provider 实现 Claude 订阅渠道。
type Provider struct {
	// sessionKeyLocks 按 accountID 隔离的互斥锁表，保证同一账号的
	// sessionKey 懒换取（session_key -> OAuth 三步流）进程内只执行一次。
	// 键为账号 ID，值为 *sync.Mutex；账号数量有限，无需淘汰。
	sessionKeyLocks sync.Map
}

// manifestModels 是订阅共享可用的模型清单。
//
// Anthropic 的"思考模式"不是独立模型名，而是请求体里的 thinking 参数；
// 这里仍然登记 "-thinking" 变体（UpstreamModel 带后缀），由 ChatStream 在
// 发请求前剥掉后缀并翻译成 thinking 参数 —— 对下游而言它就是一个普通模型，
// 路由层无需任何特殊处理。
var manifestModels = []ir.ModelMeta{
	{
		ID: channelID + "/claude-opus-4-6", UpstreamModel: "claude-opus-4-6",
		DisplayName: "Claude Opus 4.6", ContextWindow: 200000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true},
	},
	{
		ID: channelID + "/claude-opus-4-6-thinking", UpstreamModel: "claude-opus-4-6-thinking",
		DisplayName: "Claude Opus 4.6 (Thinking)", ContextWindow: 200000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true},
	},
	{
		ID: channelID + "/claude-sonnet-4-6", UpstreamModel: "claude-sonnet-4-6",
		DisplayName: "Claude Sonnet 4.6", ContextWindow: 200000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true},
	},
	{
		ID: channelID + "/claude-sonnet-4-6-thinking", UpstreamModel: "claude-sonnet-4-6-thinking",
		DisplayName: "Claude Sonnet 4.6 (Thinking)", ContextWindow: 200000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true},
	},
	{
		ID: channelID + "/claude-haiku-4-5", UpstreamModel: "claude-haiku-4-5",
		DisplayName: "Claude Haiku 4.5", ContextWindow: 200000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true},
	},
	{
		ID: channelID + "/claude-haiku-4-5-thinking", UpstreamModel: "claude-haiku-4-5-thinking",
		DisplayName: "Claude Haiku 4.5 (Thinking)", ContextWindow: 200000,
		Capabilities: ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true},
	},
}

// Manifest 声明渠道元数据与登录方式。
func (p *Provider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:          channelID,
		DisplayName: "Claude 订阅",
		Family:      "claude",
		RegionTag:   "GLOBAL",
		BrandColor:  "#D97757",
		Description: "Claude Pro/Max 订阅共享与 Anthropic 官方 API Key 两种形态：一键导入本机 Claude Code 的 OAuth 登录态（~/.claude/.credentials.json），也支持手动粘贴 access_token / refresh_token、sessionKey Cookie，或粘贴 sk-ant-api… 按量 API Key（走 x-api-key）。",
		// 订阅端点只在国际侧存在，必须优先走配置的代理池。
		NetworkPolicy: provider.NetPolicyProxyFirst,
		SupportedAuths: []provider.AuthMethod{
			provider.AuthLocalImport,
			provider.AuthOAuthPKCE,
			provider.AuthManualInput,
		},
		// 浏览器授权：Claude Code 公开客户端，固定 localhost:54545 回调，
		// token 端点只收 JSON 体且要求回传 state。
		OAuth: &provider.OAuthConfig{
			AuthorizeURL: "https://claude.ai/oauth/authorize",
			TokenURL:     "https://platform.claude.com/v1/oauth/token",
			ClientID:     defaultClientID,
			Scopes: []string{
				"user:profile", "user:inference", "user:sessions:claude_code",
				"user:mcp_servers", "user:file_upload",
			},
			RedirectPath:     "/callback",
			RedirectHost:     "localhost",
			RedirectPort:     54545,
			TokenFormat:      "json",
			ExtraAuthParams:  map[string]string{"code": "true"},
			ExtraCredentials: map[string]string{"oauth_client_id": defaultClientID},
		},
		AuthFormSchema: []provider.FormFieldSchema{
			{
				Key: "api_key", Label: "Anthropic API Key（可选）", Type: "password",
				Placeholder: "sk-ant-api03-...",
				Help:        "官方按量 API Key；填了就走 x-api-key，不再需要订阅 Token",
			},
			{
				Key: "access_token", Label: "Access Token", Type: "password",
				Placeholder: "sk-ant-oat01-...",
				Help:        "订阅 OAuth access_token；与 Session Key 二选一；本机导入会自动填好",
			},
			{
				Key: "refresh_token", Label: "Refresh Token", Type: "password",
				Placeholder: "sk-ant-ort01-...",
				Help:        "提供后网关会在过期前自动续期；本机导入会自动填好",
			},
			{
				Key: "session_key", Label: "Session Key（可选，自动换 OAuth）", Type: "password",
				Placeholder: "sk-ant-sid01-...",
				Help:        "从浏览器 claude.ai 的 Cookie 里复制 sessionKey，网关自动完成 OAuth 换取，无需手动抓 token",
			},
			{
				Key: "oauth_client_id", Label: "OAuth Client ID（可选）", Type: "text",
				Help: "刷新令牌时使用的客户端 ID；留空使用 Claude Code 官方公开 client_id",
			},
			{
				Key: "base_url", Label: "API Base URL（可选）", Type: "text",
				Placeholder: "https://api.anthropic.com",
				Help:        "自建中转 / 企业网关才需要；留空使用官方端点",
			},
		},
		Models:              manifestModels,
		RequiredMiddlewares: []string{"stateful_session"},
		Capabilities: provider.CapabilitySet{
			Chat:        true,
			LocalImport: true,
			Quota:       true,
			Refresh:     true,
		},
	}
}

// 编译期断言：本渠道实现的能力集合，漏实现直接在编译期暴露。
var (
	_ provider.BaseProvider           = (*Provider)(nil)
	_ provider.ChatCapability         = (*Provider)(nil)
	_ provider.LocalImportCapability  = (*Provider)(nil)
	_ provider.QuotaCapability        = (*Provider)(nil)
	_ provider.TokenRefreshCapability = (*Provider)(nil)
)
