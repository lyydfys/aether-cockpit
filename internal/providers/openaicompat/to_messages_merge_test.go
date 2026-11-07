package openaicompat

import (
	"testing"

	"aether-cockpit/internal/core/ir"
)

// DeepSeek thinking 模式的工具调用回合：入站解析出的条目顺序是
// Reasoning → Message(assistant) → FunctionCall → FunctionCallOutput，
// 而发回上游时必须是 **一条** assistant 消息带着 reasoning_content + tool_calls，
// 后面紧跟 tool 消息。拆成两条会 400。
func TestToMessagesMergesReasoningWithToolCalls(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "查一下天气"},
			{Type: ir.ItemTypeReasoning, Role: "assistant", Text: "我需要调用工具"},
			{Type: ir.ItemTypeFunctionCall, CallID: "c1", Name: "get_weather", Arguments: `{"city":"北京"}`},
			{Type: ir.ItemTypeFunctionCallOutput, CallID: "c1", Output: "晴 25C"},
		},
	}

	msgs := toMessages(req)
	if len(msgs) != 3 {
		t.Fatalf("消息数 = %d，want 3（user / assistant(含思考+tool_calls) / tool）: %#v", len(msgs), msgs)
	}

	asst := msgs[1]
	if asst["role"] != "assistant" {
		t.Fatalf("第二条应是 assistant，实际 %v", asst["role"])
	}
	if asst["reasoning_content"] != "我需要调用工具" {
		t.Fatalf("reasoning_content 缺失或不对: %#v", asst["reasoning_content"])
	}
	// reasoning 与 tool_calls 必须在同一条消息里 —— 这是 DeepSeek 的硬要求。
	if _, ok := asst["tool_calls"]; !ok {
		t.Fatalf("tool_calls 应和 reasoning_content 在同一条 assistant 消息里: %#v", asst)
	}
	if asst["content"] != nil {
		t.Fatalf("带 tool_calls 且无文本时 content 应为 nil，实际 %#v", asst["content"])
	}

	// tool 消息紧跟其后。
	if msgs[2]["role"] != "tool" || msgs[2]["tool_call_id"] != "c1" {
		t.Fatalf("第三条应是 tool 结果: %#v", msgs[2])
	}
}

// 纯文本回复（无 tool_calls）也要保留 reasoning_content。
func TestToMessagesKeepsReasoningWithoutToolCalls(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeReasoning, Role: "assistant", Text: "想一想"},
			{Type: ir.ItemTypeMessage, Role: "assistant", Text: "答案是 42"},
		},
	}
	msgs := toMessages(req)
	if len(msgs) != 1 {
		t.Fatalf("消息数 = %d，want 1（thinking + 文本应合成一条）: %#v", len(msgs), msgs)
	}
	if msgs[0]["reasoning_content"] != "想一想" || msgs[0]["content"] != "答案是 42" {
		t.Fatalf("合并结果不对: %#v", msgs[0])
	}
}