package sessions

import "testing"

func TestParseClaudeLines(t *testing.T) {
	user := `{"type":"user","message":{"role":"user","content":"你好，帮我重构"}}`
	parts := parseLine("claude-code", user)
	if len(parts) != 1 || parts[0].Role != "user" || parts[0].Text != "你好，帮我重构" {
		t.Fatalf("user = %+v", parts)
	}

	assistant := `{"type":"assistant","message":{"role":"assistant","model":"claude-sonnet-4-6","content":[` +
		`{"type":"thinking","thinking":"先看结构"},` +
		`{"type":"text","text":"好的"},` +
		`{"type":"tool_use","name":"read_file","input":{"path":"a.go"}}]}}`
	parts = parseLine("claude-code", assistant)
	if len(parts) != 3 {
		t.Fatalf("assistant parts = %+v", parts)
	}
	if parts[0].Kind != "thinking" || parts[1].Text != "好的" || parts[1].Model != "claude-sonnet-4-6" {
		t.Errorf("文本/思考解析错误: %+v", parts)
	}
	if parts[2].Kind != "tool" || parts[2].Name != "read_file" || parts[2].Text == "" {
		t.Errorf("工具调用解析错误: %+v", parts[2])
	}
}

func TestParseCodexLines(t *testing.T) {
	item := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"跑一下测试"}]}}`
	parts := parseLine("codex", item)
	if len(parts) != 1 || parts[0].Role != "user" || parts[0].Text != "跑一下测试" {
		t.Fatalf("response_item = %+v", parts)
	}

	msg := `{"type":"event_msg","payload":{"type":"agent_message","message":"测试全绿"}}`
	parts = parseLine("codex", msg)
	if len(parts) != 1 || parts[0].Role != "assistant" || parts[0].Text != "测试全绿" {
		t.Fatalf("event_msg = %+v", parts)
	}
}

func TestParseLineIgnoresNonJSON(t *testing.T) {
	if parts := parseLine("claude-code", "not json"); parts != nil {
		t.Fatalf("非 JSON 应跳过: %+v", parts)
	}
}

func TestHitInContext(t *testing.T) {
	text := `{"message":"请帮我检查 MCP 服务器 的连接是否正常，谢谢"}`
	h, ok := hitIn("s1", text, "mcp")
	if !ok {
		t.Fatal("未命中")
	}
	if h.ID != "s1" || h.Match != "MCP" {
		t.Errorf("hit = %+v", h)
	}
	if h.Before == "" || h.After == "" {
		t.Errorf("上下文缺失: %+v", h)
	}
	if _, ok := hitIn("s1", text, "不存在"); ok {
		t.Error("不该命中")
	}
}

func TestCleanEscapes(t *testing.T) {
	if got := clean(`a\nb\t\"c\"`); got != `a b "c"` {
		t.Fatalf("clean = %q", got)
	}
}
