package ir

// ModelCapabilities 描述一个模型支持哪些能力，供 Manifest 下发给前端做灰化提示。
type ModelCapabilities struct {
	ToolCall  bool `json:"tool_call"`
	Reasoning bool `json:"reasoning"`
	Vision    bool `json:"vision"`
	Streaming bool `json:"streaming"`
}

// ModelMeta 渠道暴露的模型元信息。
type ModelMeta struct {
	// ID 是对外的完整模型名，通常带渠道路由前缀，如 "workbuddy-cn/claude-4-sonnet"。
	ID string `json:"id"`
	// UpstreamModel 是发给上游的真实模型标识。
	UpstreamModel string `json:"upstream_model"`
	DisplayName   string `json:"display_name,omitempty"`
	// ContextWindow 上下文窗口，0 表示未知。
	ContextWindow int               `json:"context_window,omitempty"`
	Capabilities  ModelCapabilities `json:"capabilities"`
}

// 常用错误码。
const (
	ErrCodeNoProvider        = "no_provider_available"
	ErrCodeNoAccount         = "no_account_available"
	ErrCodeUpstreamFailed    = "upstream_failed"
	ErrCodeUpstreamAuth      = "upstream_auth_failed"
	ErrCodeUpstreamRateLimit = "upstream_rate_limited"
	ErrCodeBadRequest        = "bad_request"
	ErrCodeUnsupported       = "capability_unsupported"
	ErrCodeInternal          = "internal_error"
	// ErrCodePrivacyUnavailable 表示隐私遮蔽已开启但引擎不可用。
	// 这类请求必须拒绝发送（fail-closed），不能退化成明文上游调用。
	ErrCodePrivacyUnavailable = "privacy_unavailable"
	// ErrCodePrivacyBlocked 表示请求命中了"处置方式=拦截"的脱敏规则。
	// 与 privacy_unavailable（引擎坏了）区分开：这是用户的显式策略，不是故障。
	ErrCodePrivacyBlocked = "privacy_blocked"
	// ErrCodePreviousResponseNotFound 对齐 OpenAI Responses 的
	// previous_response_not_found：客户端引用的上一轮在网关里找不到。
	// 必须显式报错而不是静默丢历史——Codex 收到它会重开连接并把整段会话重发，
	// 静默跳过则表现为"模型突然失忆"且无从排查。
	ErrCodePreviousResponseNotFound = "previous_response_not_found"
)
