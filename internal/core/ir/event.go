package ir

// StreamEventType 是上游 Provider 吐给内核的唯一事件集合。
//
// 刻意保持极简的 4 种增量 + 生命周期事件：Provider 只需要产出这些，
// 复杂的协议差异（Responses 的具名事件树、Anthropic 的 content_block 生命周期）
// 全部由出站 transducer 自动补全。这是"新增渠道零协议负担"的关键约束。
type StreamEventType string

const (
	// EventStart 流开始。Provider 可携带 ResponseID 用于状态缝合。
	EventStart StreamEventType = "start"
	// EventReasoningDelta 思考片段。
	EventReasoningDelta StreamEventType = "reasoning_delta"
	// EventTextDelta 正文片段。
	EventTextDelta StreamEventType = "text_delta"
	// EventToolCallStart 一次工具调用开始（携带 ToolCallID 与 ToolName）。
	EventToolCallStart StreamEventType = "tool_call_start"
	// EventToolCallDelta 工具调用参数片段。
	EventToolCallDelta StreamEventType = "tool_call_delta"
	// EventUsage Token 统计，通常在流末出现一次。
	EventUsage StreamEventType = "usage"
	// EventError 上游错误。
	EventError StreamEventType = "error"
	// EventDone 流正常结束。
	EventDone StreamEventType = "done"
)

// Usage Token 用量统计。字段命名对齐 OpenAI Chat，便于出站转换。
//
// 口径统一约定（各渠道解析时必须归一到这个语义，否则成本会重复计算）：
//   - InputTokens：既未命中缓存、也未写入缓存的普通输入
//   - CachedTokens：命中 prompt cache 的输入（通常按折扣计价）
//   - CacheCreationTokens：写入 prompt cache 的输入（Anthropic 等按加价计价）
//   - OutputTokens / ReasoningTokens：输出与其中的思考子集
//   - TotalTokens：以上输入类之和 + 输出
//
// 上游若把 cached 包含在 prompt_tokens 里（OpenAI/Gemini），解析侧要先减去。
type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	TotalTokens         int `json:"total_tokens"`
	CachedTokens        int `json:"cached_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	ReasoningTokens     int `json:"reasoning_tokens,omitempty"`
}

// NormalizeTotal 按统一口径重算 TotalTokens。
func (u *Usage) NormalizeTotal() {
	u.TotalTokens = u.InputTokens + u.CachedTokens + u.CacheCreationTokens + u.OutputTokens
}

// SplitPromptCache 把"含缓存命中"的 prompt_tokens 拆出未命中部分。
//
// OpenAI / DeepSeek / Moonshot / Qwen / Trae / WorkBuddy 这类上游把 cached_tokens
// 算在 prompt_tokens 之内（是它的子集）。解析侧若把 prompt_tokens 直接当
// InputTokens 再额外记一份 CachedTokens，命中量会被算两遍：
// 总量虚增一份缓存量，命中率变成 cached/(prompt+cached)，数学上永远 < 50%。
//
// Anthropic / Gemini 口径相反（input 与 cache_read 平级，input 本就不含缓存），
// 那些渠道不要调用本函数。
func SplitPromptCache(promptTokens, cachedTokens int) int {
	if cachedTokens <= 0 || promptTokens <= cachedTokens {
		return promptTokens
	}
	return promptTokens - cachedTokens
}

// UnifiedStreamEvent 是 Provider 与内核之间唯一的事件契约。
type UnifiedStreamEvent struct {
	Type StreamEventType

	// 文本与思考增量。
	TextDelta string
	// 工具调用。
	ToolCallID string
	ToolName   string
	ArgsDelta  string
	// ThoughtSignature 附着在思考块结束时，供 StateSuture 按 CallID 缓存。
	ThoughtSignature string
	// EncryptedContent 附着在 reasoning item 结束时。
	EncryptedContent string
	// ResponseID 上游会话/响应标识。
	ResponseID string
	// Usage 仅在 EventUsage 时有值。
	Usage *Usage
	// FinishReason stop | length | tool_calls | content_filter
	FinishReason string
	// Err 仅在 EventError 时有值。
	Err error
}

// IsTerminal 判断该事件是否标志着流已结束（用于网关兜底关闭）。
func (e UnifiedStreamEvent) IsTerminal() bool {
	return e.Type == EventDone || e.Type == EventError
}
