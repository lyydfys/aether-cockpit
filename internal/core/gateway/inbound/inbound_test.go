package inbound

import (
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
)

func TestParseChatCompletions(t *testing.T) {
	body := []byte(`{
		"model": "workbuddy-cn/claude-4-sonnet",
		"stream": true,
		"temperature": 0.7,
		"max_tokens": 1024,
		"messages": [
			{"role": "system", "content": "你是助手"},
			{"role": "system", "content": "要简洁"},
			{"role": "user", "content": [
				{"type": "text", "text": "看图"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,AAA"}}
			]},
			{"role": "assistant", "content": "好的", "tool_calls": [
				{"id": "call_1", "type": "function", "function": {"name": "read", "arguments": "{\"p\":1}"}}
			]},
			{"role": "tool", "tool_call_id": "call_1", "content": "文件内容"}
		],
		"tools": [{"type": "function", "function": {"name": "read", "description": "读文件", "parameters": {"type": "object"}}}]
	}`)

	req, err := ParseChatCompletions(body, "req_1")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if req.Model != "workbuddy-cn/claude-4-sonnet" {
		t.Errorf("model = %q", req.Model)
	}
	if !req.Stream || req.Temperature == nil || *req.Temperature != 0.7 {
		t.Error("采样参数未解析")
	}
	if req.MaxOutputTokens == nil || *req.MaxOutputTokens != 1024 {
		t.Error("max_tokens 未解析")
	}
	// 两条 system 应折叠成一条 Instructions
	if req.Instructions != "你是助手\n\n要简洁" {
		t.Errorf("Instructions = %q", req.Instructions)
	}
	if len(req.Items) != 4 {
		t.Fatalf("Items = %d, want 4", len(req.Items))
	}
	if len(req.Items[0].Attachments) != 1 || req.Items[0].Attachments[0].DataB64 != "AAA" {
		t.Errorf("图片附件未解析: %+v", req.Items[0].Attachments)
	}
	if req.Items[2].Type != ir.ItemTypeFunctionCall || req.Items[2].CallID != "call_1" {
		t.Errorf("工具调用未解析: %+v", req.Items[2])
	}
	if req.Items[3].Type != ir.ItemTypeFunctionCallOutput || req.Items[3].Output != "文件内容" {
		t.Errorf("工具结果未解析: %+v", req.Items[3])
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "read" {
		t.Errorf("tools 未解析: %+v", req.Tools)
	}
}

func TestParseResponsesStringInput(t *testing.T) {
	body := []byte(`{"model":"m","input":"你好","instructions":"系统指令","store":false}`)
	req, err := ParseResponses(body, "r1")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(req.Items) != 1 || req.Items[0].Text != "你好" {
		t.Errorf("字符串 input 未转换: %+v", req.Items)
	}
	if req.StoreResponse {
		t.Error("store=false 应被尊重")
	}
	if req.Instructions != "系统指令" {
		t.Errorf("instructions = %q", req.Instructions)
	}
}

func TestParseResponsesItemArray(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"previous_response_id": "resp_1",
		"reasoning": {"effort": "high"},
		"input": [
			{"type":"message","role":"user","content":[{"type":"input_text","text":"问题"}]},
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"想过"}],"encrypted_content":"enc"},
			{"type":"function_call","id":"fc_1","call_id":"c1","name":"read","arguments":"{}"},
			{"type":"function_call_output","call_id":"c1","output":"结果"},
			{"type":"item_reference","id":"msg_old"}
		]
	}`)
	req, err := ParseResponses(body, "r2")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if req.PreviousResponseID != "resp_1" {
		t.Errorf("previous_response_id = %q", req.PreviousResponseID)
	}
	if !req.StoreResponse {
		t.Error("未显式传 store 时应默认为 true")
	}
	if req.ReasoningEffort != "high" {
		t.Errorf("reasoning effort = %q", req.ReasoningEffort)
	}
	if len(req.Items) != 5 {
		t.Fatalf("Items = %d, want 5", len(req.Items))
	}
	if req.Items[1].Type != ir.ItemTypeReasoning || req.Items[1].EncryptedContent != "enc" {
		t.Errorf("reasoning 未解析: %+v", req.Items[1])
	}
	if req.Items[2].Type != ir.ItemTypeFunctionCall || req.Items[2].CallID != "c1" {
		t.Errorf("function_call 未解析: %+v", req.Items[2])
	}
	if req.Items[3].Type != ir.ItemTypeFunctionCallOutput || req.Items[3].Output != "结果" {
		t.Errorf("function_call_output 未解析: %+v", req.Items[3])
	}
	if req.Items[4].RefID != "msg_old" {
		t.Errorf("item_reference 未记录: %+v", req.Items[4])
	}
}

func TestParseAnthropic(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"max_tokens": 2048,
		"system": [{"type":"text","text":"系统A"},{"type":"text","text":"系统B"}],
		"thinking": {"type":"enabled","budget_tokens":8000},
		"messages": [
			{"role":"user","content":"读文件"},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"我需要读","signature":"sig_abc"},
				{"type":"text","text":"好的"},
				{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"a"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"内容"}
			]}
		],
		"tools": [{"name":"read","description":"读","input_schema":{"type":"object"}}]
	}`)

	req, err := ParseAnthropic(body, "a1")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if req.Instructions != "系统A系统B" {
		t.Errorf("system 数组未拼接: %q", req.Instructions)
	}
	if req.ReasoningEffort != "medium" {
		t.Errorf("budget 8000 应映射为 medium，实际 %q", req.ReasoningEffort)
	}
	if len(req.Items) != 5 {
		t.Fatalf("Items = %d, want 5（user + thinking + text + tool_use + tool_result）", len(req.Items))
	}
	// 顺序必须是 thinking -> text -> tool_use，否则上游会拒绝续轮
	if req.Items[1].Type != ir.ItemTypeReasoning || req.Items[1].ThoughtSignature != "sig_abc" {
		t.Errorf("thinking 块签名丢失: %+v", req.Items[1])
	}
	if req.Items[2].Type != ir.ItemTypeMessage || req.Items[2].Text != "好的" {
		t.Errorf("文本块错误: %+v", req.Items[2])
	}
	if req.Items[3].Type != ir.ItemTypeFunctionCall || req.Items[3].Name != "read" {
		t.Errorf("tool_use 未解析: %+v", req.Items[3])
	}
	found := false
	for _, it := range req.Items {
		if it.Type == ir.ItemTypeFunctionCallOutput && it.CallID == "toolu_1" && it.Output == "内容" {
			found = true
		}
	}
	if !found {
		t.Error("user 消息里的 tool_result 未被提取")
	}
}

func TestParseErrorsAreTyped(t *testing.T) {
	_, err := ParseChatCompletions([]byte(`{`), "x")
	if err == nil {
		t.Fatal("畸形 JSON 应报错")
	}
	ae := ir.AsAPIError(err)
	if ae.Code != ir.ErrCodeBadRequest {
		t.Errorf("错误码 = %q, want bad_request", ae.Code)
	}

	_, err = ParseChatCompletions([]byte(`{"messages":[]}`), "x")
	if err == nil {
		t.Fatal("缺 model 应报错")
	}
}

func TestParseAnthropicCacheControl(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"max_tokens": 64,
		"system": [
			{"type":"text","text":"前缀"},
			{"type":"text","text":"可缓存系统提示","cache_control":{"type":"ephemeral"}}
		],
		"messages": [
			{"role":"user","content":[
				{"type":"text","text":"长上下文","cache_control":{"type":"ephemeral","ttl":"1h"}}
			]},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"想","signature":"sig","cache_control":{"type":"ephemeral"}},
				{"type":"tool_use","id":"toolu_1","name":"read","input":{"path":"a"},"cache_control":{"type":"ephemeral"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":"内容","cache_control":{"type":"ephemeral"}}
			]}
		],
		"tools": [{"name":"read","description":"读","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]
	}`)

	req, err := ParseAnthropic(body, "a2")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !strings.Contains(string(req.SystemCacheControl), "ephemeral") {
		t.Fatalf("system cache_control 丢失: %s", req.SystemCacheControl)
	}
	if len(req.Tools) != 1 || !strings.Contains(string(req.Tools[0].CacheControl), "ephemeral") {
		t.Fatalf("tool cache_control 丢失: %+v", req.Tools)
	}
	var sawUserText, sawThinking, sawToolUse, sawToolResult bool
	for _, it := range req.Items {
		if len(it.CacheControl) == 0 || !strings.Contains(string(it.CacheControl), "ephemeral") {
			continue
		}
		switch it.Type {
		case ir.ItemTypeMessage:
			sawUserText = true
		case ir.ItemTypeReasoning:
			sawThinking = true
		case ir.ItemTypeFunctionCall:
			sawToolUse = true
		case ir.ItemTypeFunctionCallOutput:
			sawToolResult = true
		}
	}
	if !sawUserText || !sawThinking || !sawToolUse || !sawToolResult {
		t.Fatalf("cache 断点未覆盖全部块: text=%v thinking=%v tool_use=%v tool_result=%v",
			sawUserText, sawThinking, sawToolUse, sawToolResult)
	}
}

func TestParseStructuredControls(t *testing.T) {
	chat, err := ParseChatCompletions([]byte(`{
		"model":"m","messages":[{"role":"user","content":"hi"}],
		"tool_choice":{"type":"function","function":{"name":"read"}},
		"response_format":{"type":"json_schema","json_schema":{"name":"out","schema":{"type":"object","properties":{"a":{"type":"string"}}},"strict":true}}
	}`), "r1")
	if err != nil {
		t.Fatal(err)
	}
	if chat.ToolChoice == nil || chat.ToolChoice.Mode != "function" || chat.ToolChoice.Name != "read" {
		t.Fatalf("chat tool_choice = %+v", chat.ToolChoice)
	}
	if chat.ResponseFormat == nil || chat.ResponseFormat.Type != "json_schema" ||
		chat.ResponseFormat.Name != "out" || chat.ResponseFormat.Strict == nil || !*chat.ResponseFormat.Strict ||
		!strings.Contains(string(chat.ResponseFormat.Schema), `"a"`) {
		t.Fatalf("chat response_format = %+v", chat.ResponseFormat)
	}

	resp, err := ParseResponses([]byte(`{
		"model":"m","input":"hi","tool_choice":"required",
		"text":{"format":{"type":"json_schema","name":"out","schema":{"type":"object"},"strict":false}}
	}`), "r2")
	if err != nil {
		t.Fatal(err)
	}
	if resp.ToolChoice == nil || resp.ToolChoice.Mode != "required" {
		t.Fatalf("responses tool_choice = %+v", resp.ToolChoice)
	}
	if resp.ResponseFormat == nil || resp.ResponseFormat.Name != "out" ||
		resp.ResponseFormat.Strict == nil || *resp.ResponseFormat.Strict {
		t.Fatalf("responses response_format = %+v", resp.ResponseFormat)
	}

	anth, err := ParseAnthropic([]byte(`{
		"model":"m","max_tokens":16,
		"messages":[{"role":"user","content":"hi"}],
		"tool_choice":{"type":"any","disable_parallel_tool_use":true}
	}`), "r3")
	if err != nil {
		t.Fatal(err)
	}
	if anth.ToolChoice == nil || anth.ToolChoice.Mode != "required" {
		t.Fatalf("anthropic tool_choice = %+v", anth.ToolChoice)
	}
	if anth.ParallelToolCalls == nil || *anth.ParallelToolCalls {
		t.Fatalf("disable_parallel_tool_use 未映射: %+v", anth.ParallelToolCalls)
	}
}

// 重复惩罚必须解析进 IR：丢了以后长输出会复读（客户端直连官方 API 时不复读，
// 因为参数原样送达；走网关被丢掉就成了"同一个问题两个结果"）。
func TestParseChatKeepsRepetitionPenalties(t *testing.T) {
	body := []byte(`{
		"model": "deepseek-v4.1-flash",
		"messages": [{"role": "user", "content": "hi"}],
		"frequency_penalty": 0.4,
		"presence_penalty": 0.1
	}`)
	req, err := ParseChatCompletions(body, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if req.FrequencyPenalty == nil || *req.FrequencyPenalty != 0.4 {
		t.Fatalf("FrequencyPenalty = %v, want 0.4", req.FrequencyPenalty)
	}
	if req.PresencePenalty == nil || *req.PresencePenalty != 0.1 {
		t.Fatalf("PresencePenalty = %v, want 0.1", req.PresencePenalty)
	}

	// 没传时保持 nil，不能凭空给上游塞一个 0（Sentinel 语义要保留）。
	plain, err := ParseChatCompletions([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), "req-2")
	if err != nil {
		t.Fatal(err)
	}
	if plain.FrequencyPenalty != nil || plain.PresencePenalty != nil {
		t.Fatal("未传时不应写入惩罚参数")
	}
}
