package openaicompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// newUpstream 起一个假的 OpenAI 兼容上游，返回指定的 SSE 脚本。
func newUpstream(t *testing.T, sseBody string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"mock upstream error"}}`))
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, line := range strings.SplitAfter(sseBody, "\n\n") {
			if line == "" {
				continue
			}
			_, _ = io.WriteString(w, line)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
}

func newEnv(baseURL string) *provider.AccountEnv {
	env := provider.NewAccountEnv("acc-1", "openai-compat")
	env.Credentials = map[string]string{
		"base_url": baseURL,
		"api_key":  "test-key",
	}
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	return env
}

// collect 消费事件流并聚合。
func collect(t *testing.T, ch <-chan ir.UnifiedStreamEvent) ir.AggregatedResponse {
	t.Helper()
	acc := ir.NewAccumulator()
	for ev := range ch {
		if ev.Type == ir.EventError {
			if ev.Err != nil {
				t.Fatalf("上游错误: %v", ev.Err)
			}
			t.Fatal("上游返回了空错误")
		}
		acc.Push(ev)
	}
	return acc.Result()
}

func TestChatStreamPlainText(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"role":"assistant","content":""}}]}

data: {"choices":[{"delta":{"content":"你好"}}]}

data: {"choices":[{"delta":{"content":"，世界"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}

data: [DONE]

`
	srv := newUpstream(t, sse, http.StatusOK)
	defer srv.Close()

	p := &Provider{}
	req := &ir.UnifiedChatRequest{
		Model:         "openai-compat/deepseek-chat",
		UpstreamModel: "deepseek-chat",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "你好"}},
	}

	ch, err := p.ChatStream(context.Background(), newEnv(srv.URL), req)
	if err != nil {
		t.Fatalf("调用失败: %v", err)
	}

	res := collect(t, ch)
	if res.Text != "你好，世界" {
		t.Errorf("文本 = %q", res.Text)
	}
	if res.FinishReason != "stop" {
		t.Errorf("结束原因 = %q", res.FinishReason)
	}
	if res.Usage.InputTokens != 10 || res.Usage.OutputTokens != 5 || res.Usage.TotalTokens != 15 {
		t.Errorf("用量解析错误: %+v", res.Usage)
	}
}

func TestChatStreamReasoning(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"reasoning_content":"先想一下"}}]}

data: {"choices":[{"delta":{"content":"答案"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
	srv := newUpstream(t, sse, http.StatusOK)
	defer srv.Close()

	p := &Provider{}
	req := &ir.UnifiedChatRequest{
		Model:         "openai-compat/deepseek-reasoner",
		UpstreamModel: "deepseek-reasoner",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "问题"}},
	}

	ch, err := p.ChatStream(context.Background(), newEnv(srv.URL), req)
	if err != nil {
		t.Fatal(err)
	}
	res := collect(t, ch)
	if res.ReasoningText != "先想一下" {
		t.Errorf("思考内容 = %q", res.ReasoningText)
	}
	if res.Text != "答案" {
		t.Errorf("正文 = %q", res.Text)
	}
}

// TestChatStreamToolCalls 验证多工具调用的 index 配对。
//
// 上游只在首个 delta 给 id，后续只给 index —— 这是最容易出错的点。
func TestChatStreamToolCalls(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"a.go\"}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"grep","arguments":"{\"q\":\"x\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	srv := newUpstream(t, sse, http.StatusOK)
	defer srv.Close()

	p := &Provider{}
	req := &ir.UnifiedChatRequest{
		Model:         "openai-compat/gpt-4o",
		UpstreamModel: "gpt-4o",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "读文件"}},
		Tools: []ir.UnifiedToolSpec{
			{Name: "read_file", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
	}

	ch, err := p.ChatStream(context.Background(), newEnv(srv.URL), req)
	if err != nil {
		t.Fatal(err)
	}
	res := collect(t, ch)

	if len(res.ToolCalls) != 2 {
		t.Fatalf("工具调用数 = %d, want 2（%+v）", len(res.ToolCalls), res.ToolCalls)
	}
	if res.ToolCalls[0].ID != "call_a" || res.ToolCalls[0].Name != "read_file" {
		t.Errorf("第一个调用错误: %+v", res.ToolCalls[0])
	}
	if res.ToolCalls[0].Arguments != `{"path":"a.go"}` {
		t.Errorf("参数拼接错误: %q", res.ToolCalls[0].Arguments)
	}
	if res.ToolCalls[1].ID != "call_b" || res.ToolCalls[1].Name != "grep" {
		t.Errorf("第二个调用错误: %+v", res.ToolCalls[1])
	}
	if res.FinishReason != "tool_calls" {
		t.Errorf("结束原因 = %q", res.FinishReason)
	}
}

// TestChatStreamToolCallsInterleavedFragments 验证乱序增量 + 参数交错：
// index 1 先开始、index 0 后到，两者的 arguments 分片交错到达时仍能各自拼成完整调用。
func TestChatStreamToolCallsInterleavedFragments(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"grep","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"q\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"x\"}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	srv := newUpstream(t, sse, http.StatusOK)
	defer srv.Close()

	p := &Provider{}
	req := &ir.UnifiedChatRequest{
		Model:         "openai-compat/gpt-4o",
		UpstreamModel: "gpt-4o",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "读文件并搜索"}},
		Tools: []ir.UnifiedToolSpec{
			{Name: "read_file", Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "grep", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
	}
	ch, err := p.ChatStream(context.Background(), newEnv(srv.URL), req)
	if err != nil {
		t.Fatal(err)
	}
	res := collect(t, ch)
	if len(res.ToolCalls) != 2 {
		t.Fatalf("工具调用数 = %d, want 2（%+v）", len(res.ToolCalls), res.ToolCalls)
	}
	byID := map[string]ir.ToolCall{}
	for _, tc := range res.ToolCalls {
		byID[tc.ID] = tc
	}
	if got := byID["call_a"]; got.Name != "read_file" || got.Arguments != `{"path":"a.go"}` {
		t.Fatalf("call_a = %+v", got)
	}
	if got := byID["call_b"]; got.Name != "grep" || got.Arguments != `{"q":"x"}` {
		t.Fatalf("call_b = %+v", got)
	}
}

func TestChatStreamHTTPErrors(t *testing.T) {
	cases := []struct {
		status    int
		wantCode  string
		wantRetry bool
	}{
		{http.StatusUnauthorized, ir.ErrCodeUpstreamAuth, false},
		{http.StatusTooManyRequests, ir.ErrCodeUpstreamRateLimit, true},
		{http.StatusBadRequest, ir.ErrCodeBadRequest, false},
		{http.StatusInternalServerError, ir.ErrCodeUpstreamFailed, true},
	}

	for _, c := range cases {
		t.Run(fmt.Sprintf("status_%d", c.status), func(t *testing.T) {
			srv := newUpstream(t, "", c.status)
			defer srv.Close()

			p := &Provider{}
			req := &ir.UnifiedChatRequest{
				Model:         "openai-compat/gpt-4o",
				UpstreamModel: "gpt-4o",
				Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "x"}},
			}
			_, err := p.ChatStream(context.Background(), newEnv(srv.URL), req)
			if err == nil {
				t.Fatal("应当返回错误")
			}
			ae := ir.AsAPIError(err)
			if ae.Code != c.wantCode {
				t.Errorf("错误码 = %q, want %q", ae.Code, c.wantCode)
			}
			if ae.Retryable != c.wantRetry {
				t.Errorf("Retryable = %v, want %v", ae.Retryable, c.wantRetry)
			}
		})
	}
}

// TestChatStreamRetryAfterHint 验证 429 的 Retry-After 头被填进
// ir.APIError.RetryAfter（网关 429 时用它当账号冷却时长）。
func TestChatStreamRetryAfterHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"mock upstream rate limited"}}`))
	}))
	defer srv.Close()

	p := &Provider{}
	req := &ir.UnifiedChatRequest{
		Model:         "openai-compat/gpt-4o",
		UpstreamModel: "gpt-4o",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "x"}},
	}
	_, err := p.ChatStream(context.Background(), newEnv(srv.URL), req)
	if err == nil {
		t.Fatal("应当返回错误")
	}
	ae := ir.AsAPIError(err)
	if ae.Code != ir.ErrCodeUpstreamRateLimit || !ae.Retryable {
		t.Fatalf("错误映射 = %+v", ae)
	}
	if ae.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("StatusCode = %d, want 429", ae.StatusCode())
	}
	if ae.RetryAfter < 40*time.Second || ae.RetryAfter > 44*time.Second {
		t.Fatalf("RetryAfter = %v, want ≈42s", ae.RetryAfter)
	}
}

func TestMissingCredentials(t *testing.T) {
	p := &Provider{}
	env := provider.NewAccountEnv("a", "openai-compat")
	env.Credentials = map[string]string{}

	_, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{})
	if err == nil {
		t.Fatal("缺少凭证应报错")
	}
	if ir.AsAPIError(err).Code != ir.ErrCodeUpstreamAuth {
		t.Errorf("错误码 = %q", ir.AsAPIError(err).Code)
	}
}

// TestToMessagesToolChain 验证工具调用链的消息构造。
//
// 这是最容易出错的地方：assistant 带 tool_calls 时 content 必须是 null，
// 且 tool 消息必须紧跟其后。
func TestToMessagesToolChain(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Instructions: "你是助手",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "读文件"},
			{Type: ir.ItemTypeReasoning, Role: "assistant", Text: "我需要调用工具"},
			{Type: ir.ItemTypeFunctionCall, CallID: "call_1", Name: "read_file", Arguments: `{"p":"a"}`},
			{Type: ir.ItemTypeFunctionCallOutput, CallID: "call_1", Output: "文件内容"},
			{Type: ir.ItemTypeMessage, Role: "assistant", Text: "读到了"},
		},
	}

	msgs := toMessages(req)

	if len(msgs) != 5 {
		t.Fatalf("消息数 = %d, want 5（system + user + assistant(tool_calls) + tool + assistant）", len(msgs))
	}
	if msgs[0]["role"] != "system" {
		t.Error("首条应为 system")
	}
	if msgs[2]["role"] != "assistant" {
		t.Errorf("第三条应为 assistant，实际 %v", msgs[2]["role"])
	}
	if msgs[2]["content"] != nil {
		t.Errorf("带 tool_calls 的 assistant 消息 content 必须为 nil，实际 %v", msgs[2]["content"])
	}
	if msgs[2]["tool_calls"] == nil {
		t.Error("assistant 消息缺少 tool_calls")
	}
	if msgs[2]["reasoning_content"] != "我需要调用工具" {
		t.Errorf("思考内容未挂到 assistant 消息: %v", msgs[2]["reasoning_content"])
	}
	if msgs[3]["role"] != "tool" {
		t.Errorf("第四条应为 tool，实际 %v", msgs[3]["role"])
	}
	if msgs[3]["tool_call_id"] != "call_1" {
		t.Errorf("tool_call_id = %v", msgs[3]["tool_call_id"])
	}
}

// TestParseWholeJSON 验证不支持流式、直接返回整段 JSON 的端点。
func TestParseWholeJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"完整响应","reasoning_content":"想过"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}
		}`))
	}))
	defer srv.Close()

	p := &Provider{}
	req := &ir.UnifiedChatRequest{
		Model:         "openai-compat/x",
		UpstreamModel: "x",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "x"}},
	}

	ch, err := p.ChatStream(context.Background(), newEnv(srv.URL), req)
	if err != nil {
		t.Fatal(err)
	}
	res := collect(t, ch)
	if res.Text != "完整响应" {
		t.Errorf("文本 = %q", res.Text)
	}
	if res.ReasoningText != "想过" {
		t.Errorf("思考 = %q", res.ReasoningText)
	}
	if res.Usage.TotalTokens != 7 {
		t.Errorf("用量 = %+v", res.Usage)
	}
}

// collectToolEvents 消费事件流，返回按到达顺序排列的
// (Start callID) 序列、(Delta callID, args) 序列与结束原因。
func collectToolEvents(t *testing.T, ch <-chan ir.UnifiedStreamEvent) (starts []string, deltas [][2]string, finish string) {
	t.Helper()
	for ev := range ch {
		switch ev.Type {
		case ir.EventError:
			if ev.Err != nil {
				t.Fatalf("上游错误: %v", ev.Err)
			}
			t.Fatal("上游返回了空错误")
		case ir.EventToolCallStart:
			starts = append(starts, ev.ToolCallID)
		case ir.EventToolCallDelta:
			deltas = append(deltas, [2]string{ev.ToolCallID, ev.ArgsDelta})
		case ir.EventDone:
			finish = ev.FinishReason
		}
	}
	return starts, deltas, finish
}

// TestChatStreamToolCallsOutOfOrder 验证乱序 index 容错：
// index 1 先到 -> 前驱 index 0 尚未 Start，先暂存；
// index 0 到达即构成乱序证据，holding 保持到流结束；
// 流结束 flush 时按 index 升序整段吐出（a 在 b 之前）。
func TestChatStreamToolCallsOutOfOrder(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"grep","arguments":"{\"q\":\"x\"}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	srv := newUpstream(t, sse, http.StatusOK)
	defer srv.Close()

	p := &Provider{}
	req := &ir.UnifiedChatRequest{
		Model:         "openai-compat/gpt-4o",
		UpstreamModel: "gpt-4o",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "读文件"}},
	}

	ch, err := p.ChatStream(context.Background(), newEnv(srv.URL), req)
	if err != nil {
		t.Fatal(err)
	}
	starts, deltas, finish := collectToolEvents(t, ch)

	if len(starts) != 2 || starts[0] != "call_a" || starts[1] != "call_b" {
		t.Fatalf("Start 顺序 = %v, want [call_a call_b]", starts)
	}
	if len(deltas) != 2 {
		t.Fatalf("Delta 数 = %d, want 2", len(deltas))
	}
	if deltas[0][0] != "call_a" || deltas[0][1] != `{"path":"a.go"}` {
		t.Errorf("第一个 Delta = %v, want [call_a {\"path\":\"a.go\"}]", deltas[0])
	}
	if deltas[1][0] != "call_b" || deltas[1][1] != `{"q":"x"}` {
		t.Errorf("第二个 Delta = %v, want [call_b {\"q\":\"x\"}]", deltas[1])
	}
	if finish != "tool_calls" {
		t.Errorf("结束原因 = %q", finish)
	}
}

// TestChatStreamToolCallsHeldGap 验证前驱缺失时的暂存：
// index 0 完成后直接出现 index 2（index 1 从未出现），
// index 2 必须等 flush 才吐出，保证 Start 顺序仍是升序。
func TestChatStreamToolCallsHeldGap(t *testing.T) {
	sse := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.go\"}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":2,"id":"call_c","type":"function","function":{"name":"ls","arguments":"{}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	srv := newUpstream(t, sse, http.StatusOK)
	defer srv.Close()

	p := &Provider{}
	req := &ir.UnifiedChatRequest{
		Model:         "openai-compat/gpt-4o",
		UpstreamModel: "gpt-4o",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "读文件"}},
	}

	ch, err := p.ChatStream(context.Background(), newEnv(srv.URL), req)
	if err != nil {
		t.Fatal(err)
	}
	starts, deltas, finish := collectToolEvents(t, ch)

	if len(starts) != 2 || starts[0] != "call_a" || starts[1] != "call_c" {
		t.Fatalf("Start 顺序 = %v, want [call_a call_c]", starts)
	}
	if len(deltas) != 2 || deltas[0][0] != "call_a" || deltas[1][0] != "call_c" {
		t.Fatalf("Delta 序列 = %v", deltas)
	}
	if deltas[1][1] != "{}" {
		t.Errorf("call_c 参数 = %q", deltas[1][1])
	}
	if finish != "tool_calls" {
		t.Errorf("结束原因 = %q", finish)
	}
}

func TestProviderSpecOverridesVendorBaseURLAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/custom/chat" {
			t.Errorf("path = %q, want /custom/chat", r.URL.Path)
		}
		if got := r.Header.Get("X-Vendor"); got != "yes" {
			t.Errorf("X-Vendor = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	dir := t.TempDir()
	raw := fmt.Sprintf(`{"provider_id":"deepseek","spec_version":"test","base_url":%q,"headers_template":{"X-Vendor":"yes"},"endpoints":{"chat":"/custom/chat"}}`, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "deepseek.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	old := spec.Default()
	store := spec.NewStore()
	if err := store.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	spec.SetDefault(store)
	defer spec.SetDefault(old)

	p := &Provider{cfg: Config{ID: "deepseek", BaseURL: "http://127.0.0.1:1"}}
	req := &ir.UnifiedChatRequest{
		Model: "deepseek/deepseek-chat",
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), newEnv(""), req)
	if err != nil {
		t.Fatal(err)
	}
	res := collect(t, ch)
	if res.Text != "ok" {
		t.Fatalf("text = %q", res.Text)
	}
}

func TestMaxTokensFieldForOpenAIReasoningModels(t *testing.T) {
	openai := &Provider{cfg: Config{ID: "openai"}}
	for _, m := range []string{"o3-mini", "o1", "o4-mini", "gpt-5-codex"} {
		if got := openai.maxTokensField(m); got != "max_completion_tokens" {
			t.Errorf("%s -> %s", m, got)
		}
	}
	if got := openai.maxTokensField("gpt-4o"); got != "max_tokens" {
		t.Errorf("gpt-4o -> %s", got)
	}
	if got := (&Provider{cfg: Config{ID: "deepseek"}}).maxTokensField("o3-mini"); got != "max_tokens" {
		t.Errorf("非 OpenAI 厂商不应使用 max_completion_tokens: %s", got)
	}
	n := 32
	payload := buildPayload(&ir.UnifiedChatRequest{MaxOutputTokens: &n}, "o3-mini", "max_completion_tokens")
	if payload["max_completion_tokens"] != 32 {
		t.Fatalf("payload = %v", payload)
	}
	if _, ok := payload["max_tokens"]; ok {
		t.Fatalf("不应同时发送 max_tokens: %v", payload)
	}
}

func TestChatStreamUnwrapsDataChunks(t *testing.T) {
	sse := `data: {"success":true,"data":{"choices":[{"delta":{"content":"包在 data 里"}}]}}

data: {"success":true,"data":{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}}

data: [DONE]

`
	srv := newUpstream(t, sse, http.StatusOK)
	defer srv.Close()
	p := &Provider{cfg: Config{ID: "openai-compat"}}
	req := &ir.UnifiedChatRequest{
		Model: "openai-compat/test-model", UpstreamModel: "test-model",
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), newEnv(srv.URL), req)
	if err != nil {
		t.Fatal(err)
	}
	res := collect(t, ch)
	if res.Text != "包在 data 里" || res.Usage.TotalTokens != 7 || res.FinishReason != "stop" {
		t.Fatalf("res = %+v", res)
	}
}

func TestChatStreamUnwrapsWholeJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"choices":[{"message":{"content":"整段 JSON"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}}`)
	}))
	defer srv.Close()
	p := &Provider{cfg: Config{ID: "openai-compat"}}
	req := &ir.UnifiedChatRequest{
		Model: "openai-compat/test-model", UpstreamModel: "test-model",
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), newEnv(srv.URL), req)
	if err != nil {
		t.Fatal(err)
	}
	res := collect(t, ch)
	if res.Text != "整段 JSON" || res.Usage.TotalTokens != 7 {
		t.Fatalf("res = %+v", res)
	}
}

func TestListModelsFromUpstream(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-5"},{"id":"glm-5"}]}`)
	}))
	defer srv.Close()

	p := &Provider{cfg: Config{ID: "test-live", BaseURL: srv.URL}}
	env := provider.NewAccountEnv("acc", "test-live")
	env.Credentials = map[string]string{"api_key": "sk-test"}
	env.HTTPClient = srv.Client()
	if !p.SupportsModelList(env) {
		t.Fatal("普通 OpenAI 兼容渠道应支持模型列表")
	}
	ids, err := p.ListModels(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "gpt-5" || ids[1] != "glm-5" {
		t.Fatalf("ids = %v", ids)
	}
	if gotPath != "/models" || gotAuth != "Bearer sk-test" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if (&Provider{cfg: Config{ID: "azure", Azure: true}}).SupportsModelList(env) {
		t.Fatal("Azure 不应走标准 /models")
	}
}
