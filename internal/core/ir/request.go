package ir

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Protocol 标识下游客户端使用的入站协议。
type Protocol string

const (
	// ProtocolOpenAIChat 对应 POST /v1/chat/completions。
	ProtocolOpenAIChat Protocol = "openai_chat"
	// ProtocolOpenAIResponses 对应 POST /v1/responses。
	ProtocolOpenAIResponses Protocol = "openai_responses"
	// ProtocolAnthropic 对应 POST /v1/messages。
	ProtocolAnthropic Protocol = "anthropic_messages"
	// ProtocolGemini 对应 POST /v1beta/models/{model}:generateContent / :streamGenerateContent。
	ProtocolGemini Protocol = "gemini"
)

// UnifiedToolSpec 归一化后的工具定义。
type UnifiedToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"` // JSON Schema
	// CacheControl 保留 Anthropic 工具定义的缓存断点。
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

// ToolChoice 归一化工具选择策略。
//
// Mode: auto | none | required | function；function 时 Name 必填。
type ToolChoice struct {
	Mode string `json:"mode"`
	Name string `json:"name,omitempty"`
}

// ResponseFormat 归一化结构化输出约束。
//
// Type: text | json_object | json_schema；json_schema 时 Name/Schema 携带定义。
type ResponseFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Strict *bool           `json:"strict,omitempty"`
}

// UnifiedChatRequest 是全系统唯一的内部请求标准结构。
//
// 上游 Provider 只依赖这个结构，因此新增下游协议或新增渠道都不会引起对方的改动。
type UnifiedChatRequest struct {
	RequestID          string   `json:"request_id"`
	DownstreamProtocol Protocol `json:"downstream_protocol"`
	Model              string   `json:"model"` // 原始下游模型名，如 "workbuddy-cn/claude-4-sonnet" 或 "auto/claude-sonnet"

	// ProviderID / UpstreamModel 由路由层在选定渠道后回填。
	ProviderID    string `json:"provider_id,omitempty"`
	UpstreamModel string `json:"upstream_model,omitempty"`

	// --- 有状态链（Responses API）---

	// PreviousResponseID 下游传入的上一轮 response id。
	// 非空时由 StateSuture 中间件回填历史并锁定原账号。
	PreviousResponseID string `json:"previous_response_id,omitempty"`
	// StoreResponse 对齐 Responses API 的 store 字段。
	// 注意：store=false 时服务端不应留存状态，桥接必须降级为无状态。
	StoreResponse bool `json:"store_response"`

	// --- 归一化上下文 ---

	Instructions string `json:"instructions,omitempty"` // 归一化的 system / developer prompt
	// SystemCacheControl 保留 Anthropic system 块的 prompt caching 断点。
	SystemCacheControl json.RawMessage   `json:"system_cache_control,omitempty"`
	Items              []UnifiedItem     `json:"items"`
	Tools              []UnifiedToolSpec `json:"tools,omitempty"`

	// --- 采样与推理控制 ---

	Stream      bool     `json:"stream"`
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	// FrequencyPenalty / PresencePenalty 是 OpenAI 的重复惩罚。此前没有建模，
	// 客户端传了也被丢掉：长输出容易滑进复读循环（同一片段刷几百遍），
	// 而直连官方 API 时参数原样送达，所以不会复读。
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	MaxOutputTokens  *int     `json:"max_output_tokens,omitempty"`
	StopSequences    []string `json:"stop_sequences,omitempty"`
	ReasoningEffort  string   `json:"reasoning_effort,omitempty"` // low | medium | high
	// EffortAuto 表示 ReasoningEffort 是**网关根据请求内容猜出来的**，
	// 而不是客户端或用户规则明确要求的。
	//
	// 这类"猜出来的"档位不能下发给上游：
	//   - 它会让推理模型额外消耗输出预算（reasoning token 计入 max_tokens），
	//     客户端按自己的 max_tokens 精打细算时，工具调用的 JSON 会被截断，
	//     而同样的请求直连上游却是正常的；
	//   - 客户端没要求的事，网关不该替它决定。
	EffortAuto        bool            `json:"-"`
	ParallelToolCalls *bool           `json:"parallel_tool_calls,omitempty"`
	ToolChoice        *ToolChoice     `json:"tool_choice,omitempty"`
	ResponseFormat    *ResponseFormat `json:"response_format,omitempty"`

	// --- 路由控制（由网关内部填充，不下发给上游）---

	// SessionKey 是会话粘性指纹，由 StateSuture 计算。
	SessionKey string `json:"-"`
	// Meta 保留各渠道入站解析时无法归一化的原始字段，供 Fast-Path 透传使用。
	Meta map[string]any `json:"-"`
}

// LastUserText 返回最近一条用户消息的纯文本，用于会话指纹计算。
func (r *UnifiedChatRequest) LastUserText() string {
	for i := len(r.Items) - 1; i >= 0; i-- {
		if r.Items[i].IsUserTurn() {
			return r.Items[i].Text
		}
	}
	return ""
}

// ToSimpleMessages 把 IR 退化为传统的 [{role, content}] 列表。
//
// 这是给"只支持纯文本对话"的轻量渠道的一键兼容入口：
// reasoning 被丢弃，function_call / function_call_output 被折叠成带标记的文本，
// 使得模型至少能看到工具调用发生过，而不是上下文断裂。
func (r *UnifiedChatRequest) ToSimpleMessages() []SimpleMessage {
	var out []SimpleMessage
	if strings.TrimSpace(r.Instructions) != "" {
		out = append(out, SimpleMessage{Role: "system", Content: r.Instructions})
	}
	for _, it := range r.Items {
		switch it.Type {
		case ItemTypeMessage:
			if it.IsBlank() {
				continue
			}
			out = append(out, SimpleMessage{Role: normalizeRole(it.Role), Content: it.Text})

		case ItemTypeReasoning:
			// 思考明文对不支持思考块的渠道无意义，直接丢弃以免污染上下文。

		case ItemTypeFunctionCall:
			out = append(out, SimpleMessage{
				Role:      "assistant",
				Content:   fmt.Sprintf("[tool_call:%s] %s", it.Name, it.Arguments),
				ToolCalls: []SimpleToolCall{{ID: it.CallID, Name: it.Name, Arguments: it.Arguments}},
			})

		case ItemTypeFunctionCallOutput:
			out = append(out, SimpleMessage{
				Role:       "tool",
				Content:    fmt.Sprintf("[tool_result:%s] %s", it.CallID, it.Output),
				ToolCallID: it.CallID,
			})
		}
	}
	return out
}

// MergeAdjacentSameRole 合并相邻同角色消息，供不支持连续同角色消息的上游使用。
func MergeAdjacentSameRole(msgs []SimpleMessage) []SimpleMessage {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]SimpleMessage, 0, len(msgs))
	for _, m := range msgs {
		if n := len(out); n > 0 && out[n-1].Role == m.Role && m.Role != "tool" && len(m.ToolCalls) == 0 {
			out[n-1].Content = strings.TrimRight(out[n-1].Content, "\n") + "\n\n" + m.Content
			continue
		}
		out = append(out, m)
	}
	return out
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system", "developer":
		return "system"
	case "assistant", "model":
		return "assistant"
	case "tool", "function":
		return "tool"
	default:
		return "user"
	}
}

// Clone 深拷贝请求，供中间件在修改前做快照（如粘性路由失败重试）。
func (r *UnifiedChatRequest) Clone() *UnifiedChatRequest {
	if r == nil {
		return nil
	}
	cp := *r
	cp.Items = make([]UnifiedItem, len(r.Items))
	for i, it := range r.Items {
		cp.Items[i] = it.Clone()
	}
	if len(r.Tools) > 0 {
		cp.Tools = make([]UnifiedToolSpec, len(r.Tools))
		copy(cp.Tools, r.Tools)
	}
	if len(r.StopSequences) > 0 {
		cp.StopSequences = append([]string(nil), r.StopSequences...)
	}
	if r.Temperature != nil {
		v := *r.Temperature
		cp.Temperature = &v
	}
	if r.TopP != nil {
		v := *r.TopP
		cp.TopP = &v
	}
	if r.MaxOutputTokens != nil {
		v := *r.MaxOutputTokens
		cp.MaxOutputTokens = &v
	}
	return &cp
}
