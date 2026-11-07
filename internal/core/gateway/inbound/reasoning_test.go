package inbound

import (
	"testing"

	"aether-cockpit/internal/core/ir"
)

// DeepSeek thinking 模式的工具回合要求把上一轮 reasoning_content 原样带回，
// 否则 400：`The reasoning_content in the thinking mode must be passed back to the API`。
// 入站若在解析时就丢掉它，出站（openaicompat.toMessages）根本没有数据可回填。
func TestParseChatKeepsReasoningContent(t *testing.T) {
	body := []byte(`{
		"model": "deepseek-chat",
		"messages": [
			{"role":"user","content":"读一下 a.txt"},
			{"role":"assistant","content":"好的","reasoning_content":"我需要调用 read_file",
			 "tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"p\":\"a\"}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"文件内容"}
		]
	}`)

	req, err := ParseChatCompletions(body, "r1")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	var reasoning []ir.UnifiedItem
	for _, it := range req.Items {
		if it.Type == ir.ItemTypeReasoning {
			reasoning = append(reasoning, it)
		}
	}
	if len(reasoning) != 1 {
		t.Fatalf("reasoning item 数 = %d, want 1（Items=%+v）", len(reasoning), req.Items)
	}
	if reasoning[0].Text != "我需要调用 read_file" || reasoning[0].Role != "assistant" {
		t.Fatalf("reasoning = %+v", reasoning[0])
	}

	// reasoning 必须在 tool_calls / tool 消息之前，否则 outbound 聚合顺序会错。
	ri, ci := -1, -1
	for i, it := range req.Items {
		switch it.Type {
		case ir.ItemTypeReasoning:
			if ri < 0 {
				ri = i
			}
		case ir.ItemTypeFunctionCall:
			if ci < 0 {
				ci = i
			}
		}
	}
	if ri < 0 || ci < 0 || ri > ci {
		t.Fatalf("reasoning 必须先于 tool_calls：reasoning@%d tool_call@%d", ri, ci)
	}
}

// OpenRouter 的别名 reasoning 同样要保留。
func TestParseChatKeepsReasoningAlias(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"messages": [
			{"role":"user","content":"hi"},
			{"role":"assistant","content":"","reasoning":"先想一想",
			 "tool_calls":[{"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]}
		]
	}`)
	req, err := ParseChatCompletions(body, "r2")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	found := false
	for _, it := range req.Items {
		if it.Type == ir.ItemTypeReasoning && it.Text == "先想一想" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasoning 别名未保留：%+v", req.Items)
	}
}

// 空白 reasoning 不应产生空 item。
func TestParseChatSkipsBlankReasoning(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"messages": [
			{"role":"user","content":"hi"},
			{"role":"assistant","content":"x","reasoning_content":"   "}
		]
	}`)
	req, err := ParseChatCompletions(body, "r3")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	for _, it := range req.Items {
		if it.Type == ir.ItemTypeReasoning {
			t.Fatalf("空白 reasoning 不应产生 item：%+v", it)
		}
	}
}

// Anthropic 入站：thinking 块的签名与明文都要保留（续轮必需）。
func TestParseAnthropicKeepsThinkingSignature(t *testing.T) {
	body := []byte(`{
		"model":"m","max_tokens":1024,
		"messages":[
			{"role":"user","content":"hi"},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"想想","signature":"sig-1"},
				{"type":"redacted_thinking","data":"ENC"},
				{"type":"tool_use","id":"t1","name":"f","input":{}}
			]}
		]
	}`)
	req, err := ParseAnthropic(body, "a2")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	var sig, enc string
	for _, it := range req.Items {
		if it.Type != ir.ItemTypeReasoning {
			continue
		}
		if it.Text == "想想" {
			sig = it.ThoughtSignature
		}
		if it.EncryptedContent == "ENC" {
			enc = it.EncryptedContent
		}
	}
	if sig != "sig-1" {
		t.Fatalf("thinking 签名丢失: %q", sig)
	}
	if enc != "ENC" {
		t.Fatalf("redacted_thinking 载荷丢失")
	}
}

// Responses 入站：reasoning summary 与 content 两种形态都要能解析。
func TestParseResponsesKeepsReasoning(t *testing.T) {
	body := []byte(`{
		"model":"m",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
			{"type":"reasoning","summary":[{"type":"summary_text","text":"摘要思考"}]},
			{"type":"reasoning","content":[{"type":"reasoning_text","text":"正文思考"}]},
			{"type":"reasoning","summary":[]}
		]
	}`)
	req, err := ParseResponses(body, "rp1")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	var texts []string
	for _, it := range req.Items {
		if it.Type == ir.ItemTypeReasoning {
			texts = append(texts, it.Text)
		}
	}
	if len(texts) != 2 {
		t.Fatalf("reasoning 文本 = %v, want 2 条（空思考块应丢弃）", texts)
	}
	if texts[0] != "摘要思考" || texts[1] != "正文思考" {
		t.Fatalf("reasoning 文本 = %v", texts)
	}
}

// Gemini 入站：thought=true 的段落要落成 reasoning，不能混进正文。
func TestParseGeminiKeepsThought(t *testing.T) {
	body := []byte(`{
		"contents":[
			{"role":"user","parts":[{"text":"hi"}]},
			{"role":"model","parts":[
				{"text":"先想","thought":true},
				{"text":"答案"}
			]}
		]
	}`)
	req, err := ParseGemini(body, "g1", "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	var reasoning, messages int
	for _, it := range req.Items {
		switch it.Type {
		case ir.ItemTypeReasoning:
			reasoning++
			if it.Text != "先想" {
				t.Fatalf("reasoning 文本 = %q", it.Text)
			}
		case ir.ItemTypeMessage:
			messages++
		}
	}
	if reasoning != 1 || messages != 2 {
		t.Fatalf("reasoning=%d messages=%d（want 1 / 2）", reasoning, messages)
	}
}
