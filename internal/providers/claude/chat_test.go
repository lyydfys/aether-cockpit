package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// TestBuildRequestBody 覆盖 IR -> Anthropic 请求体的核心转换：
// system 合并、消息交替合并、thinking/tool_result 块顺序、max_tokens 默认值。
func TestBuildRequestBody(t *testing.T) {
	temp := 0.4
	maxTokens := 256
	req := &ir.UnifiedChatRequest{
		Instructions:    "你是助手",
		Stream:          true,
		Temperature:     &temp,
		MaxOutputTokens: &maxTokens,
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "你好"},
			{Type: ir.ItemTypeReasoning, Text: "先想一下", ThoughtSignature: "sig-1"},
			{Type: ir.ItemTypeFunctionCall, CallID: "call-1", Name: "get_weather", Arguments: `{"city":"SZ"}`},
			{Type: ir.ItemTypeFunctionCallOutput, CallID: "call-1", Output: "晴"},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "谢谢"},
		},
		Tools: []ir.UnifiedToolSpec{{
			Name:        "get_weather",
			Description: "查询天气",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
	}

	body := buildRequestBody(req, "claude-sonnet-4-6", false)

	if body["model"] != "claude-sonnet-4-6" {
		t.Fatalf("model = %v", body["model"])
	}
	// 显式给了 MaxOutputTokens 就必须透传。
	if body["max_tokens"] != 256 {
		t.Fatalf("max_tokens = %v", body["max_tokens"])
	}
	if body["stream"] != true {
		t.Fatalf("stream = %v", body["stream"])
	}
	if body["temperature"] != 0.4 {
		t.Fatalf("temperature = %v", body["temperature"])
	}

	// system 是顶层字段：Instructions 与残留 system 消息合并成一个 text 块。
	system, _ := body["system"].([]any)
	if len(system) != 1 {
		t.Fatalf("system = %+v", body["system"])
	}
	sysBlock, _ := system[0].(map[string]any)
	if sysBlock["type"] != "text" || sysBlock["text"] != "你是助手" {
		t.Fatalf("system block = %+v", sysBlock)
	}

	messages, _ := body["messages"].([]any)
	// 工具结果（user）与紧随其后的用户文本必须合并成同一条 user 消息
	//（tool_result 在前），否则违反 user/assistant 严格交替。
	if len(messages) != 3 {
		t.Fatalf("messages = %+v", messages)
	}
	m0, _ := messages[0].(map[string]any)
	if m0["role"] != "user" {
		t.Fatalf("messages[0] = %+v", m0)
	}
	// reasoning + function_call 是同一轮 assistant 输出，必须合并进同一条消息，
	// 且 thinking 块排在 tool_use 之前。
	m1, _ := messages[1].(map[string]any)
	if m1["role"] != "assistant" {
		t.Fatalf("messages[1] = %+v", m1)
	}
	blocks1, _ := m1["content"].([]any)
	if len(blocks1) != 2 {
		t.Fatalf("assistant content = %+v", blocks1)
	}
	thinking, _ := blocks1[0].(map[string]any)
	if thinking["type"] != "thinking" || thinking["thinking"] != "先想一下" || thinking["signature"] != "sig-1" {
		t.Fatalf("thinking block = %+v", thinking)
	}
	toolUse, _ := blocks1[1].(map[string]any)
	if toolUse["type"] != "tool_use" || toolUse["id"] != "call-1" || toolUse["name"] != "get_weather" {
		t.Fatalf("tool_use = %+v", toolUse)
	}
	if input, ok := toolUse["input"].(map[string]any); !ok || input["city"] != "SZ" {
		t.Fatalf("tool input = %+v", toolUse["input"])
	}

	// 工具结果在 user 消息里，tool_result 块必须在最前；后面跟着合并进来的用户文本。
	m2, _ := messages[2].(map[string]any)
	if m2["role"] != "user" {
		t.Fatalf("messages[2] = %+v", m2)
	}
	blocks2, _ := m2["content"].([]any)
	if len(blocks2) != 2 {
		t.Fatalf("user content = %+v", blocks2)
	}
	result, _ := blocks2[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "call-1" {
		t.Fatalf("tool_result = %+v", result)
	}
	followUp, _ := blocks2[1].(map[string]any)
	if followUp["type"] != "text" || followUp["text"] != "谢谢" {
		t.Fatalf("follow-up text = %+v", followUp)
	}

	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %+v", tools)
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != "get_weather" || tool["input_schema"] == nil {
		t.Fatalf("tool = %+v", tool)
	}
}

// TestBuildRequestBodySystemMerge 验证 Instructions 与 system 角色消息的合并。
func TestBuildRequestBodySystemMerge(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Instructions: "全局提示词",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "system", Text: "补充规则"},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
		},
	}
	body := buildRequestBody(req, "claude-sonnet-4-6", false)
	system, _ := body["system"].([]any)
	if len(system) != 1 {
		t.Fatalf("system = %+v", system)
	}
	block, _ := system[0].(map[string]any)
	if block["text"] != "全局提示词\n\n补充规则" {
		t.Fatalf("system text = %v", block["text"])
	}
	// system 消息不能混进 messages。
	messages, _ := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %+v", messages)
	}
}

// TestBuildRequestBodyDefaults 验证 max_tokens 默认值与空工具列表。
func TestBuildRequestBodyDefaults(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Stream: false,
		Items:  []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "ping"}},
	}
	body := buildRequestBody(req, "claude-sonnet-4-6", false)
	if body["max_tokens"] != defaultMaxTokens {
		t.Fatalf("max_tokens 默认值 = %v, want %d", body["max_tokens"], defaultMaxTokens)
	}
	if body["stream"] != false {
		t.Fatalf("stream = %v", body["stream"])
	}
	if _, exists := body["tools"]; exists {
		t.Fatal("没有工具时不应出现 tools 字段")
	}
	if _, exists := body["system"]; exists {
		t.Fatal("没有 system 提示时不应出现 system 字段")
	}
}

// TestBuildRequestBodyThinking 验证 -thinking 变体的转换：
// 剥掉模型后缀、注入 thinking 参数、保证 max_tokens 大于预算、丢弃 temperature。
func TestBuildRequestBodyThinking(t *testing.T) {
	temp := 0.4
	maxTokens := 2048
	req := &ir.UnifiedChatRequest{
		Stream:          true,
		Temperature:     &temp,
		MaxOutputTokens: &maxTokens,
		ReasoningEffort: "high",
		Items:           []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "难题"}},
	}
	body := buildRequestBody(req, "claude-sonnet-4-6", true)

	thinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("thinking = %+v", body["thinking"])
	}
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != 16384 {
		t.Fatalf("thinking = %+v", thinking)
	}
	// max_tokens(2048) 小于预算时必须上调，否则上游 400。
	if mt := body["max_tokens"].(int); mt <= 16384 {
		t.Fatalf("max_tokens = %v 应大于思考预算", body["max_tokens"])
	}
	if _, exists := body["temperature"]; exists {
		t.Fatal("thinking 开启时不应携带 temperature（上游互斥）")
	}
	if _, exists := body["top_p"]; exists {
		t.Fatal("thinking 开启时不应携带 top_p")
	}
}

func TestResolveModel(t *testing.T) {
	model, thinking := resolveModel(&ir.UnifiedChatRequest{
		UpstreamModel: "claude-sonnet-4-6-thinking",
	})
	if model != "claude-sonnet-4-6" || !thinking {
		t.Fatalf("model=%q thinking=%v", model, thinking)
	}
	model, thinking = resolveModel(&ir.UnifiedChatRequest{
		UpstreamModel: "",
		Model:         "claude-sub/claude-haiku-4-5",
	})
	if model != "claude-haiku-4-5" || thinking {
		t.Fatalf("model=%q thinking=%v", model, thinking)
	}
}

// feedSSE 把若干 SSE 帧写给 http 响应。
func feedSSE(w http.ResponseWriter, frames []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, frame := range frames {
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", frameEvent(frame), frame)
	}
}

// frameEvent 从线缆 JSON 里取 type 当 SSE 事件名（贴近真实上游的写法）。
func frameEvent(frame string) string {
	var m map[string]any
	_ = json.Unmarshal([]byte(frame), &m)
	name, _ := m["type"].(string)
	return name
}

// TestChatStreamParsesAnthropicSSE 用假 SSE 流验证事件序列与 usage。
func TestChatStreamAPIKeyMode(t *testing.T) {
	var gotKey, gotAuth, gotBeta, gotVersion string
	frames := []string{
		`{"type":"message_start","message":{"id":"msg_api","usage":{"input_tokens":3,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"api-key-ok"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
		`{"type":"message_stop"}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		gotVersion = r.Header.Get("anthropic-version")
		feedSSE(w, frames)
	}))
	defer srv.Close()

	p := &Provider{}
	env := provider.NewAccountEnv("acc-key", channelID)
	env.Credentials = map[string]string{
		"api_key":  "sk-ant-api03-test",
		"base_url": srv.URL,
	}
	env.HTTPClient = srv.Client()

	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model:         channelID + "/claude-sonnet-4-6",
		UpstreamModel: "claude-sonnet-4-6",
		Stream:        true,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text string
	for ev := range ch {
		if ev.Type == ir.EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
		if ev.Type == ir.EventTextDelta {
			text += ev.TextDelta
		}
	}
	if text != "api-key-ok" {
		t.Errorf("text = %q", text)
	}
	if gotKey != "sk-ant-api03-test" {
		t.Errorf("x-api-key = %q", gotKey)
	}
	if gotAuth != "" || gotBeta != "" {
		t.Errorf("API key mode must not send OAuth headers, got Authorization=%q beta=%q", gotAuth, gotBeta)
	}
	if gotVersion != anthropicVersion {
		t.Errorf("anthropic-version = %q", gotVersion)
	}
}

func TestChatStreamParsesAnthropicSSE(t *testing.T) {
	var (
		gotPath     string
		gotAuth     string
		gotBeta     string
		gotVersion  string
		gotUA       string
		gotBody     map[string]any
		gotBodyOnce bool
	)

	frames := []string{
		`{"type":"message_start","message":{"id":"msg_01","usage":{"input_tokens":10,"cache_read_input_tokens":4,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"想"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"你好"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{}}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
		`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"SZ\"}"}}`,
		`{"type":"content_block_stop","index":2}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42}}`,
		`{"type":"message_stop"}`,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		gotVersion = r.Header.Get("anthropic-version")
		gotUA = r.Header.Get("User-Agent")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		gotBodyOnce = true
		feedSSE(w, frames)
	}))
	defer srv.Close()

	p := &Provider{}
	env := provider.NewAccountEnv("acc-1", channelID)
	env.Credentials = map[string]string{
		"access_token": "tk",
		"expires_at":   time.Now().Add(2 * time.Hour).Format(time.RFC3339),
		"base_url":     srv.URL,
	}
	env.HTTPClient = srv.Client()

	req := &ir.UnifiedChatRequest{
		Model:         channelID + "/claude-sonnet-4-6",
		UpstreamModel: "claude-sonnet-4-6",
		Stream:        true,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}

	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}

	var (
		starts    []ir.UnifiedStreamEvent
		text      string
		reasoning string
		toolName  string
		toolArgs  string
		sig       string
		usage     *ir.Usage
		finish    string
	)
	for ev := range ch {
		switch ev.Type {
		case ir.EventStart:
			starts = append(starts, ev)
		case ir.EventReasoningDelta:
			reasoning += ev.TextDelta
		case ir.EventTextDelta:
			text += ev.TextDelta
		case ir.EventToolCallStart:
			toolName = ev.ToolName
			sig = ev.ThoughtSignature
		case ir.EventToolCallDelta:
			toolArgs += ev.ArgsDelta
		case ir.EventUsage:
			usage = ev.Usage
		case ir.EventDone:
			finish = ev.FinishReason
		case ir.EventError:
			t.Fatalf("意外错误事件: %v", ev.Err)
		}
	}

	if len(starts) < 1 || starts[len(starts)-1].ResponseID != "msg_01" {
		t.Fatalf("starts = %+v", starts)
	}
	if reasoning != "想" || text != "你好" {
		t.Fatalf("reasoning=%q text=%q", reasoning, text)
	}
	if toolName != "get_weather" || toolArgs != `{"city":"SZ"}` {
		t.Fatalf("tool=%q args=%q", toolName, toolArgs)
	}
	// thinking 签名必须附着在工具调用 Start 上（聚合器按 ToolCallID 键控）。
	if sig != "sig-1" {
		t.Fatalf("thought signature = %q", sig)
	}
	if usage == nil || usage.InputTokens != 10 || usage.OutputTokens != 42 ||
		usage.CachedTokens != 4 || usage.TotalTokens != 56 {
		t.Fatalf("usage = %+v", usage)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish = %q", finish)
	}

	// 请求头与请求体契约。
	if gotPath != "/v1/messages" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tk" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotBeta != defaultBetaHeader {
		t.Fatalf("anthropic-beta = %q", gotBeta)
	}
	if gotVersion != anthropicVersion {
		t.Fatalf("anthropic-version = %q", gotVersion)
	}
	// UA 地板：默认 UA 必须是 claude-cli/2.1.258（低于上游版本地板会被
	// 判非正版并持续 429 且无 reset 头）。
	if !strings.HasPrefix(gotUA, "claude-cli/2.1.258") {
		t.Fatalf("user-agent = %q", gotUA)
	}
	if !gotBodyOnce {
		t.Fatal("未捕获请求体")
	}
	if gotBody["model"] != "claude-sonnet-4-6" || gotBody["stream"] != true {
		t.Fatalf("body = %+v", gotBody)
	}
	// JSON 反序列化后数字都是 float64。
	if gotBody["max_tokens"] != float64(defaultMaxTokens) {
		t.Fatalf("max_tokens = %v", gotBody["max_tokens"])
	}
	messages, _ := gotBody["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %+v", messages)
	}
}

// TestChatStreamParsesNonStream 验证 stream=false 走 JSON 解析路径。
func TestChatStreamParsesNonStream(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"msg_02","type":"message","role":"assistant",
			"content":[
				{"type":"text","text":"pong"}
			],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":5,"output_tokens":2}
		}`)
	}))
	defer srv.Close()

	p := &Provider{}
	env := provider.NewAccountEnv("acc-2", channelID)
	env.Credentials = map[string]string{
		"access_token": "tk",
		"base_url":     srv.URL,
	}
	env.HTTPClient = srv.Client()

	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model:         channelID + "/claude-haiku-4-5",
		UpstreamModel: "claude-haiku-4-5",
		Stream:        false,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "ping"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text, finish, respID string
	var usage *ir.Usage
	for ev := range ch {
		switch ev.Type {
		case ir.EventStart:
			respID = ev.ResponseID
		case ir.EventTextDelta:
			text += ev.TextDelta
		case ir.EventUsage:
			usage = ev.Usage
		case ir.EventDone:
			finish = ev.FinishReason
		case ir.EventError:
			t.Fatalf("意外错误事件: %v", ev.Err)
		}
	}
	if respID != "msg_02" || text != "pong" || finish != "stop" {
		t.Fatalf("respID=%q text=%q finish=%q", respID, text, finish)
	}
	if usage == nil || usage.InputTokens != 5 || usage.OutputTokens != 2 || usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v", usage)
	}
	if gotBody["stream"] != false {
		t.Fatalf("请求体 stream = %v，应为 false", gotBody["stream"])
	}
}

// TestChatStreamNonStreamWithToolCall 非流式响应里的 tool_use 块。
func TestChatStreamNonStreamWithToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{
			"id":"msg_03","type":"message","role":"assistant",
			"content":[
				{"type":"text","text":"查一下"},
				{"type":"tool_use","id":"toolu_9","name":"get_weather","input":{"city":"SZ"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":8,"output_tokens":9}
		}`)
	}))
	defer srv.Close()

	p := &Provider{}
	env := provider.NewAccountEnv("acc-3", channelID)
	env.Credentials = map[string]string{"access_token": "tk", "base_url": srv.URL}
	env.HTTPClient = srv.Client()

	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		UpstreamModel: "claude-sonnet-4-6",
		Stream:        false,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "天气"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text, toolName, toolArgs, finish string
	for ev := range ch {
		switch ev.Type {
		case ir.EventTextDelta:
			text += ev.TextDelta
		case ir.EventToolCallStart:
			toolName = ev.ToolName
		case ir.EventToolCallDelta:
			toolArgs += ev.ArgsDelta
		case ir.EventDone:
			finish = ev.FinishReason
		}
	}
	if text != "查一下" || toolName != "get_weather" || toolArgs != `{"city":"SZ"}` {
		t.Fatalf("text=%q tool=%q args=%q", text, toolName, toolArgs)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish = %q", finish)
	}
}

// TestChatStreamMapsUpstreamError 验证 HTTP 层错误经 ChatStream 透出。
func TestChatStreamMapsUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"Number of requests has exceeded your rate limit"}}`)
	}))
	defer srv.Close()

	p := &Provider{}
	env := provider.NewAccountEnv("acc-4", channelID)
	env.Credentials = map[string]string{"access_token": "tk", "base_url": srv.URL}
	env.HTTPClient = srv.Client()

	_, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		UpstreamModel: "claude-sonnet-4-6",
		Stream:        true,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeUpstreamRateLimit {
		t.Fatalf("err = %v", err)
	}
	if !ae.Retryable {
		t.Fatal("429 应标记为可重试")
	}
	if !strings.Contains(ae.Message, "rate limit") {
		t.Fatalf("message = %q", ae.Message)
	}
}

// TestChatStreamRetryAfterHint 验证 429 的限流窗口被填进 ir.APIError.RetryAfter
// （网关用它在 429 时做账号冷却）。只断言范围，避免时钟抖动。
func TestChatStreamRetryAfterHint(t *testing.T) {
	cases := []struct {
		name  string
		apply func(h http.Header)
	}{
		{
			name:  "Retry-After 秒数",
			apply: func(h http.Header) { h.Set("Retry-After", "42") },
		},
		{
			name: "anthropic 5h 窗口 RFC3339",
			apply: func(h http.Header) {
				h.Set("anthropic-ratelimit-unified-5h-reset", time.Now().Add(42*time.Second).Format(time.RFC3339))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tc.apply(w.Header())
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"rate_limit_error","message":"Number of requests has exceeded your rate limit"}}`)
			}))
			defer srv.Close()

			p := &Provider{}
			env := provider.NewAccountEnv("acc-5", channelID)
			env.Credentials = map[string]string{"access_token": "tk", "base_url": srv.URL}
			env.HTTPClient = srv.Client()

			_, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
				UpstreamModel: "claude-sonnet-4-6",
				Stream:        true,
				Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
			})
			ae := ir.AsAPIError(err)
			if ae == nil || ae.Code != ir.ErrCodeUpstreamRateLimit {
				t.Fatalf("err = %v", err)
			}
			if ae.StatusCode() != http.StatusTooManyRequests {
				t.Fatalf("StatusCode = %d, want 429", ae.StatusCode())
			}
			if ae.RetryAfter < 40*time.Second || ae.RetryAfter > 44*time.Second {
				t.Fatalf("RetryAfter = %v, want ≈42s", ae.RetryAfter)
			}
		})
	}
}

// TestSanitizeBetaHeaders beta 头与 body 的双向能力对齐（纯函数契约）。
func TestSanitizeBetaHeaders(t *testing.T) {
	thinkingBody := map[string]any{
		"model":      "claude-sonnet-4-6",
		"thinking":   map[string]any{"type": "enabled", "budget_tokens": 4096},
		"max_tokens": 8192,
	}

	t.Run("头缺 thinking beta -> 剥 body.thinking", func(t *testing.T) {
		body := cloneBody(thinkingBody)
		gotBeta, gotBody := sanitizeBetaHeaders(oauthBetaHeader, body)
		if gotBeta != oauthBetaHeader {
			t.Fatalf("beta 被改动: %q", gotBeta)
		}
		if _, exists := gotBody["thinking"]; exists {
			t.Fatalf("thinking 未被剥离: %+v", gotBody)
		}
		if gotBody["model"] != "claude-sonnet-4-6" {
			t.Fatalf("其它字段被动到: %+v", gotBody)
		}
	})

	t.Run("头含 thinking beta -> 保留 body.thinking", func(t *testing.T) {
		body := cloneBody(thinkingBody)
		_, gotBody := sanitizeBetaHeaders(defaultBetaHeader, body)
		if _, exists := gotBody["thinking"]; !exists {
			t.Fatalf("thinking 被误剥: %+v", gotBody)
		}
	})

	t.Run("头缺 context-management -> 剥 body.context_management", func(t *testing.T) {
		body := map[string]any{
			"model":              "claude-sonnet-4-6",
			"context_management": map[string]any{"edits": []any{}},
		}
		_, gotBody := sanitizeBetaHeaders(oauthBetaHeader, body)
		if _, exists := gotBody["context_management"]; exists {
			t.Fatalf("context_management 未被剥离: %+v", gotBody)
		}
	})

	t.Run("头含能力而 body 无字段 -> 不动", func(t *testing.T) {
		body := map[string]any{"model": "claude-sonnet-4-6"}
		_, gotBody := sanitizeBetaHeaders(defaultBetaHeader, body)
		if len(gotBody) != 1 || gotBody["model"] != "claude-sonnet-4-6" {
			t.Fatalf("body 被动到: %+v", gotBody)
		}
	})

	t.Run("nil body 不 panic", func(t *testing.T) {
		gotBeta, gotBody := sanitizeBetaHeaders(oauthBetaHeader, nil)
		if gotBeta != oauthBetaHeader || gotBody != nil {
			t.Fatalf("beta=%q body=%+v", gotBeta, gotBody)
		}
	})
}

// cloneBody 深拷贝测试 body，避免用例间共享 map。
func cloneBody(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// TestChatStreamStripsThinkingWithoutBeta ChatStream 端到端 sanitize：
// anthropic_beta 覆盖未声明 interleaved-thinking 时，thinking 变体的
// body.thinking 被剥掉（上游对未声明能力的字段直接 400）；
// 默认 beta 头已声明，thinking 字段保留。
func TestChatStreamStripsThinkingWithoutBeta(t *testing.T) {
	cases := []struct {
		name         string
		beta         string
		wantThinking bool
	}{
		{"默认 beta 头保留 thinking", "", true},
		{"覆盖缺 thinking beta 被剥", oauthBetaHeader, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &gotBody)
				_, _ = io.WriteString(w, `{"id":"m","type":"message","content":[{"type":"text","text":"x"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
			}))
			defer srv.Close()

			p := &Provider{}
			env := provider.NewAccountEnv("acc-sanitize", channelID)
			env.Credentials = map[string]string{"access_token": "tk", "base_url": srv.URL}
			if tc.beta != "" {
				env.Credentials["anthropic_beta"] = tc.beta
			}
			env.HTTPClient = srv.Client()

			ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
				UpstreamModel:   "claude-sonnet-4-6-thinking",
				Stream:          false,
				ReasoningEffort: "low",
				Items:           []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			for ev := range ch {
				if ev.Type == ir.EventError {
					t.Fatalf("事件错误: %v", ev.Err)
				}
			}
			_, hasThinking := gotBody["thinking"]
			if hasThinking != tc.wantThinking {
				t.Fatalf("body.thinking 存在 = %v, want %v（body=%+v）", hasThinking, tc.wantThinking, gotBody)
			}
		})
	}
}

// TestChatStreamUserAgentOverride credentials.user_agent 覆盖默认 UA。
func TestChatStreamUserAgentOverride(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = io.WriteString(w, `{"id":"m","type":"message","content":[{"type":"text","text":"x"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer srv.Close()

	p := &Provider{}
	env := provider.NewAccountEnv("acc-ua", channelID)
	env.Credentials = map[string]string{
		"access_token": "tk",
		"base_url":     srv.URL,
		"user_agent":   "claude-cli/9.9.9 (custom, relay)",
	}
	env.HTTPClient = srv.Client()

	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		UpstreamModel: "claude-sonnet-4-6",
		Stream:        false,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range ch { // 排空事件即可
	}
	if gotUA != "claude-cli/9.9.9 (custom, relay)" {
		t.Fatalf("user-agent = %q", gotUA)
	}
}

// TestMapUpstreamError 错误码映射表。
func TestMapUpstreamError(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		code      string
		retryable bool
		contains  string
	}{
		{"401", 401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			ir.ErrCodeUpstreamAuth, false, "invalid x-api-key"},
		{"403", 403, `{"type":"error","error":{"type":"permission_error","message":"denied"}}`,
			ir.ErrCodeUpstreamAuth, false, "denied"},
		{"429", 429, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`,
			ir.ErrCodeUpstreamRateLimit, true, "slow down"},
		{"400", 400, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: Field required"}}`,
			ir.ErrCodeBadRequest, false, "max_tokens"},
		{"500", 500, `{"type":"error","error":{"type":"api_error","message":"boom"}}`,
			ir.ErrCodeUpstreamFailed, true, "boom"},
		{"529 overloaded", 529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			ir.ErrCodeUpstreamFailed, true, "Overloaded"},
		{"非 JSON body", 418, `<html>teapot</html>`,
			ir.ErrCodeUpstreamFailed, false, "teapot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ae := mapUpstreamError(tc.status, tc.body, nil)
			if ae.Code != tc.code {
				t.Fatalf("code = %q, want %q", ae.Code, tc.code)
			}
			if ae.Retryable != tc.retryable {
				t.Fatalf("retryable = %v, want %v", ae.Retryable, tc.retryable)
			}
			if !strings.Contains(ae.Message, tc.contains) {
				t.Fatalf("message = %q 缺少 %q", ae.Message, tc.contains)
			}
		})
	}
}

// TestMapStreamError 流内 error 事件的映射。
func TestMapStreamError(t *testing.T) {
	ae := mapStreamError(map[string]any{"type": "rate_limit_error", "message": "too fast"})
	if ae.Code != ir.ErrCodeUpstreamRateLimit || !ae.Retryable {
		t.Fatalf("ae = %+v", ae)
	}
	ae = mapStreamError(map[string]any{"type": "overloaded_error", "message": "Overloaded"})
	if ae.Code != ir.ErrCodeUpstreamFailed || !ae.Retryable {
		t.Fatalf("ae = %+v", ae)
	}
	ae = mapStreamError(map[string]any{"type": "authentication_error", "message": "expired"})
	if ae.Code != ir.ErrCodeUpstreamAuth {
		t.Fatalf("ae = %+v", ae)
	}
}

// TestParseSSEInStreamError 验证流中途出现 error 事件时产出 EventError。
func TestParseSSEInStreamError(t *testing.T) {
	frames := []string{
		`{"type":"message_start","message":{"id":"msg_x","usage":{"input_tokens":1}}}`,
		`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		feedSSE(w, frames)
	}))
	defer srv.Close()

	p := &Provider{}
	env := provider.NewAccountEnv("acc-5", channelID)
	env.Credentials = map[string]string{"access_token": "tk", "base_url": srv.URL}
	env.HTTPClient = srv.Client()

	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		UpstreamModel: "claude-sonnet-4-6",
		Stream:        true,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sawErr, sawDone := false, false
	for ev := range ch {
		switch ev.Type {
		case ir.EventError:
			sawErr = true
			ae := ir.AsAPIError(ev.Err)
			if ae == nil || ae.Code != ir.ErrCodeUpstreamFailed || !ae.Retryable {
				t.Fatalf("error = %+v", ae)
			}
		case ir.EventDone:
			sawDone = true
		}
	}
	if !sawErr {
		t.Fatal("未产出 EventError")
	}
	if sawDone {
		t.Fatal("错误后不应再产出 Done")
	}
}

// TestMapFinishReason 结束原因映射表。
func TestMapFinishReason(t *testing.T) {
	cases := map[string]string{
		"end_turn":      "stop",
		"stop_sequence": "stop",
		"max_tokens":    "length",
		"tool_use":      "tool_calls",
		"refusal":       "content_filter",
		"pause_turn":    "stop",
		"whatever":      "stop",
	}
	for in, want := range cases {
		if got := mapFinishReason(in); got != want {
			t.Errorf("mapFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// cache_control 断点必须跨 IR 保留：system / 文本 / 思考 / 工具 / 工具结果。
func TestBuildRequestBodyCacheControl(t *testing.T) {
	cc := json.RawMessage(`{"type":"ephemeral","ttl":"1h"}`)
	req := &ir.UnifiedChatRequest{
		Instructions:       "sys",
		SystemCacheControl: cc,
		Stream:             true,
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi", CacheControl: cc},
			{Type: ir.ItemTypeReasoning, Role: "assistant", EncryptedContent: "enc", CacheControl: cc},
			{Type: ir.ItemTypeFunctionCall, Role: "assistant", CallID: "toolu_1", Name: "read", Arguments: `{"p":"a"}`, CacheControl: cc},
			{Type: ir.ItemTypeFunctionCallOutput, CallID: "toolu_1", Output: "ok", CacheControl: cc},
		},
		Tools: []ir.UnifiedToolSpec{{
			Name: "read", Parameters: json.RawMessage(`{"type":"object"}`), CacheControl: cc,
		}},
	}
	body := buildRequestBody(req, "m", false)

	system := body["system"].([]any)[0].(map[string]any)
	if system["cache_control"] == nil {
		t.Fatalf("system cache_control 丢失: %v", system)
	}
	tool := body["tools"].([]any)[0].(map[string]any)
	if tool["cache_control"] == nil {
		t.Fatalf("tool cache_control 丢失: %v", tool)
	}

	seen := map[string]bool{}
	for _, rawMsg := range body["messages"].([]any) {
		msg := rawMsg.(map[string]any)
		for _, rawBlock := range msg["content"].([]any) {
			block := rawBlock.(map[string]any)
			if block["cache_control"] == nil {
				continue
			}
			if typ, _ := block["type"].(string); typ != "" {
				seen[typ] = true
			}
		}
	}
	for _, typ := range []string{"text", "redacted_thinking", "tool_use", "tool_result"} {
		if !seen[typ] {
			t.Fatalf("%s 缺少 cache_control；seen=%v", typ, seen)
		}
	}
}

func TestAnthropicToolChoiceMapping(t *testing.T) {
	parallel := false
	cases := []struct {
		in   *ir.ToolChoice
		typ  string
		name string
	}{
		{&ir.ToolChoice{Mode: "auto"}, "auto", ""},
		{&ir.ToolChoice{Mode: "none"}, "none", ""},
		{&ir.ToolChoice{Mode: "required"}, "any", ""},
		{&ir.ToolChoice{Mode: "function", Name: "read"}, "tool", "read"},
	}
	for _, c := range cases {
		body := buildRequestBody(&ir.UnifiedChatRequest{
			Stream: true, ToolChoice: c.in,
			Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
		}, "m", false)
		tc, _ := body["tool_choice"].(map[string]any)
		if tc["type"] != c.typ || tc["name"] != c.name && c.name != "" {
			t.Fatalf("%s => %v", c.in.Mode, tc)
		}
	}

	body := buildRequestBody(&ir.UnifiedChatRequest{
		Stream: true, ParallelToolCalls: &parallel,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}, "m", false)
	tc, _ := body["tool_choice"].(map[string]any)
	if tc["type"] != "auto" || tc["disable_parallel_tool_use"] != true {
		t.Fatalf("parallel=false => %v", tc)
	}
}
