package openaicompat

import (
	"testing"

	"aether-cockpit/internal/core/gateway/inbound"
	"aether-cockpit/internal/core/ir"
)

// 端到端回归：DeepSeek thinking 模式的工具回合。
//
// 入站（OpenAI Chat 协议）收到上一轮的 assistant 消息带 reasoning_content
// 与 tool_calls；出站重建请求体时必须把 reasoning_content 原样挂回同一条
// assistant 消息，否则上游 400：
// `The reasoning_content in the thinking mode must be passed back to the API`。
func TestReasoningContentRoundTrip(t *testing.T) {
	body := []byte(`{
		"model": "deepseek-chat",
		"messages": [
			{"role":"user","content":"读一下 a.txt"},
			{"role":"assistant","content":"","reasoning_content":"我需要读文件",
			 "tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"p\":\"a.txt\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"hello"},
			{"role":"user","content":"继续"}
		]
	}`)

	req, err := inbound.ParseChatCompletions(body, "req-1")
	if err != nil {
		t.Fatalf("入站解析失败: %v", err)
	}
	msgs := toMessages(req)

	// 找到带 tool_calls 的那条 assistant 消息。
	var callMsg map[string]any
	for _, m := range msgs {
		if m["tool_calls"] != nil {
			callMsg = m
			break
		}
	}
	if callMsg == nil {
		t.Fatalf("没有生成带 tool_calls 的 assistant 消息: %+v", msgs)
	}
	if got := callMsg["reasoning_content"]; got != "我需要读文件" {
		t.Fatalf("reasoning_content 未原样带回上游: %v（消息 %+v）", got, callMsg)
	}
	if callMsg["content"] != nil {
		t.Fatalf("带 tool_calls 的 assistant content 必须为 nil，实际 %v", callMsg["content"])
	}
	// tool 消息必须紧跟其后。
	found := false
	for i, m := range msgs {
		if m["role"] == "tool" && m["tool_call_id"] == "call_1" {
			if i == 0 || msgs[i-1]["tool_calls"] == nil {
				t.Fatalf("tool 消息没有紧跟 assistant(tool_calls)")
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("缺少 tool 结果消息: %+v", msgs)
	}
}

// 没有 reasoning_content 的普通回合不应凭空多出该字段。
func TestReasoningContentAbsentWhenNotProvided(t *testing.T) {
	req := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
		{Type: ir.ItemTypeFunctionCall, CallID: "c1", Name: "f", Arguments: `{}`},
		{Type: ir.ItemTypeFunctionCallOutput, CallID: "c1", Output: "ok"},
	}}
	msgs := toMessages(req)
	for _, m := range msgs {
		if _, ok := m["reasoning_content"]; ok {
			t.Fatalf("未提供思考内容时不应写入 reasoning_content: %+v", m)
		}
	}
}
