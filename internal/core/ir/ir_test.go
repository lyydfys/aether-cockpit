package ir

import "testing"

func TestAccumulator(t *testing.T) {
	acc := NewAccumulator()
	acc.Push(UnifiedStreamEvent{Type: EventStart, ResponseID: "sess_1"})
	acc.Push(UnifiedStreamEvent{Type: EventReasoningDelta, TextDelta: "思考"})
	acc.Push(UnifiedStreamEvent{Type: EventTextDelta, TextDelta: "正文"})
	acc.Push(UnifiedStreamEvent{Type: EventToolCallStart, ToolCallID: "c1", ToolName: "read_file", ThoughtSignature: "sig1"})
	acc.Push(UnifiedStreamEvent{Type: EventToolCallDelta, ArgsDelta: `{"a"`})
	acc.Push(UnifiedStreamEvent{Type: EventToolCallDelta, ArgsDelta: `:1}`})
	acc.Push(UnifiedStreamEvent{Type: EventToolCallStart, ToolCallID: "c2", ToolName: "grep"})
	acc.Push(UnifiedStreamEvent{Type: EventToolCallDelta, ArgsDelta: `{}`})
	acc.Push(UnifiedStreamEvent{Type: EventUsage, Usage: &Usage{InputTokens: 10, OutputTokens: 5}})

	res := acc.Result()
	if res.ResponseID != "sess_1" {
		t.Errorf("ResponseID = %q", res.ResponseID)
	}
	if res.ReasoningText != "思考" || res.Text != "正文" {
		t.Errorf("文本聚合错误: %+v", res)
	}
	if len(res.ToolCalls) != 2 {
		t.Fatalf("工具调用数 = %d", len(res.ToolCalls))
	}
	if res.ToolCalls[0].Arguments != `{"a":1}` {
		t.Errorf("参数拼接错误: %q", res.ToolCalls[0].Arguments)
	}
	if res.ToolCalls[1].Name != "grep" {
		t.Errorf("第二个工具名 = %q", res.ToolCalls[1].Name)
	}
	if res.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", res.FinishReason)
	}
	if res.Usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", res.Usage.TotalTokens)
	}
	if res.ThoughtSignatures["c1"] != "sig1" {
		t.Errorf("签名未记录: %+v", res.ThoughtSignatures)
	}
}

func TestAccumulatorToolDeltaWithoutStart(t *testing.T) {
	// 上游不规范：只发 Delta 不发 Start。聚合器要能兜底。
	acc := NewAccumulator()
	acc.Push(UnifiedStreamEvent{Type: EventToolCallDelta, ToolCallID: "c9", ToolName: "x", ArgsDelta: `{"k":1}`})
	res := acc.Result()
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Arguments != `{"k":1}` {
		t.Errorf("兜底失败: %+v", res.ToolCalls)
	}
}

func TestToSimpleMessages(t *testing.T) {
	req := &UnifiedChatRequest{
		Instructions: "系统提示",
		Items: []UnifiedItem{
			{Type: ItemTypeMessage, Role: "user", Text: "问题"},
			{Type: ItemTypeReasoning, Role: "assistant", Text: "思考过程"},
			{Type: ItemTypeFunctionCall, CallID: "c1", Name: "read", Arguments: `{"p":1}`},
			{Type: ItemTypeFunctionCallOutput, CallID: "c1", Output: "内容"},
			{Type: ItemTypeMessage, Role: "assistant", Text: "回答"},
		},
	}
	msgs := req.ToSimpleMessages()

	if len(msgs) != 5 {
		t.Fatalf("消息数 = %d, want 5（reasoning 被丢弃）", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "系统提示" {
		t.Errorf("首条应为 system: %+v", msgs[0])
	}
	for _, m := range msgs {
		if m.Role == "system" && m.Content == "思考过程" {
			t.Error("reasoning 不应进入普通消息")
		}
	}
	if msgs[2].Role != "assistant" || len(msgs[2].ToolCalls) != 1 {
		t.Errorf("工具调用消息错误: %+v", msgs[2])
	}
	if msgs[3].ToolCallID != "c1" {
		t.Errorf("工具结果关联 ID 错误: %+v", msgs[3])
	}
}

func TestMergeAdjacentSameRole(t *testing.T) {
	in := []SimpleMessage{
		{Role: "user", Content: "a"},
		{Role: "user", Content: "b"},
		{Role: "assistant", Content: "c"},
		{Role: "tool", Content: "d", ToolCallID: "x"},
		{Role: "tool", Content: "e", ToolCallID: "y"},
	}
	out := MergeAdjacentSameRole(in)
	if len(out) != 4 {
		t.Fatalf("合并后长度 = %d, want 4（tool 不合并）", len(out))
	}
	if out[0].Content != "a\n\nb" {
		t.Errorf("合并内容 = %q", out[0].Content)
	}
}

func TestCloneIsDeep(t *testing.T) {
	req := &UnifiedChatRequest{
		Items: []UnifiedItem{{Type: ItemTypeMessage, Role: "user", Text: "x", Attachments: []MediaAttachment{{URL: "u"}}}},
		Tools: []UnifiedToolSpec{{Name: "t"}},
	}
	cp := req.Clone()
	cp.Items[0].Text = "changed"
	cp.Items[0].Attachments[0].URL = "v"
	cp.Tools[0].Name = "changed"

	if req.Items[0].Text != "x" {
		t.Error("Items 未深拷贝")
	}
	if req.Items[0].Attachments[0].URL != "u" {
		t.Error("Attachments 未深拷贝")
	}
	if req.Tools[0].Name != "t" {
		t.Error("Tools 未深拷贝")
	}
}

func TestAPIErrorStatus(t *testing.T) {
	cases := map[string]int{
		ErrCodeBadRequest:        400,
		ErrCodeUpstreamAuth:      401,
		ErrCodeNoAccount:         503,
		ErrCodeUpstreamRateLimit: 429,
		ErrCodeUpstreamFailed:    502,
	}
	for code, want := range cases {
		e := NewAPIError(code, "x")
		if got := e.StatusCode(); got != want {
			t.Errorf("%s: status = %d, want %d", code, got, want)
		}
	}
}
