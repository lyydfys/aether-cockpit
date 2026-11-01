package qoder

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func decodeQoderBody(t *testing.T, wire []byte) string {
	t.Helper()
	s := swapThirds(string(wire))
	stdAlphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var b strings.Builder
	for _, ch := range s {
		if ch == '$' {
			b.WriteByte('=')
			continue
		}
		idx := strings.IndexRune(qoderAlphabet, ch)
		if idx < 0 {
			t.Fatalf("未知的 Qoder 编码字符: %q", ch)
		}
		b.WriteByte(stdAlphabet[idx])
	}
	raw, err := base64.StdEncoding.DecodeString(b.String())
	if err != nil {
		t.Fatalf("base64 解码失败: %v", err)
	}
	return string(raw)
}

func TestQoderBodyCodecRoundTrip(t *testing.T) {
	plain := `{"model":"auto","messages":[{"role":"user","content":"你好, Qoder!"}]}`
	wire := encodeQoderBody([]byte(plain))
	if string(wire) == plain {
		t.Fatal("编码后不应与明文相同")
	}
	if got := decodeQoderBody(t, wire); got != plain {
		t.Fatalf("往返失败:\n got=%s\nwant=%s", got, plain)
	}
}

func TestBuildPayload(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Instructions: "你是助手",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "你好"},
			{Type: ir.ItemTypeReasoning, Text: "想一下"},
			{Type: ir.ItemTypeFunctionCall, CallID: "call-1", Name: "get_weather", Arguments: `{"city":"SZ"}`},
			{Type: ir.ItemTypeFunctionCallOutput, CallID: "call-1", Output: "晴"},
		},
		Tools: []ir.UnifiedToolSpec{{Name: "get_weather", Description: "天气", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	payload := buildPayload(req, "Qwen3.8-Max")
	if payload["model"] != "Qwen3.8-Max" || payload["stream"] != true {
		t.Fatalf("payload = %+v", payload)
	}
	meta, _ := payload["metadata"].(map[string]any)
	ctx, _ := meta["context"].(map[string]any)
	if ctx["client_type"] != "10" || ctx["os_type"] != "windows" {
		t.Fatalf("metadata.context = %+v", ctx)
	}
	if sid, _ := ctx["session_id"].(string); !strings.HasPrefix(sid, "-") {
		t.Fatalf("session_id = %q", sid)
	}
	msgs, _ := payload["messages"].([]map[string]any)
	if len(msgs) < 4 {
		t.Fatalf("messages = %+v", msgs)
	}
	if msgs[0]["role"] != "system" {
		t.Fatalf("system = %+v", msgs[0])
	}
	if msgs[1]["role"] != "user" || msgs[1]["content"] != "你好" {
		t.Fatalf("user = %+v", msgs[1])
	}
	if len(payload["tools"].([]map[string]any)) != 1 {
		t.Fatalf("tools = %+v", payload["tools"])
	}
}

func TestChatStreamSignedSSE(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		plain := decodeQoderBody(t, raw)
		_ = json.Unmarshal([]byte(plain), &gotBody)

		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"statusCodeValue":200,"body":"{\"choices\":[{\"delta\":{\"content\":\"你\"}}]}"}`,
			`{"statusCodeValue":200,"body":"{\"choices\":[{\"delta\":{\"reasoning_content\":\"想\"}}]}"}`,
			`{"statusCodeValue":200,"body":"{\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_x\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\":\\\"SZ\\\"}\"}}]}}]}"}`,
			`{"statusCodeValue":200,"body":"{\"choices\":[{\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4,\"total_tokens\":7,\"completion_tokens_details\":{\"reasoning_tokens\":2}}}"}`,
		}
		for _, f := range frames {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", f)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newProvider(profile{
		id: "qoder-test", displayName: "Qoder Test", baseURL: "http://127.0.0.1:1",
		openAPI: srv.URL, cosyVer: "1.1.77",
	})
	env := provider.NewAccountEnv("acc-1", "qoder-test")
	env.Credentials = map[string]string{
		"token":      "tk",
		"user_id":    "uid-1",
		"machine_id": "mid-1",
		"user_name":  "测试",
		"base_url":   srv.URL,
	}
	env.HTTPClient = srv.Client()

	req := &ir.UnifiedChatRequest{
		Model: "qoder-test/auto", UpstreamModel: "auto", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}

	var text, reasoning, finish, toolName, toolArgs string
	var usage *ir.Usage
	for ev := range ch {
		switch ev.Type {
		case ir.EventTextDelta:
			text += ev.TextDelta
		case ir.EventReasoningDelta:
			reasoning += ev.TextDelta
		case ir.EventToolCallStart:
			toolName = ev.ToolName
		case ir.EventToolCallDelta:
			toolArgs += ev.ArgsDelta
		case ir.EventUsage:
			usage = ev.Usage
		case ir.EventDone:
			finish = ev.FinishReason
		}
	}

	if text != "你" || reasoning != "想" {
		t.Fatalf("text=%q reasoning=%q", text, reasoning)
	}
	if toolName != "get_weather" || toolArgs != `{"city":"SZ"}` {
		t.Fatalf("tool=%q args=%q", toolName, toolArgs)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish=%q", finish)
	}
	if usage == nil || usage.InputTokens != 3 || usage.OutputTokens != 4 || usage.ReasoningTokens != 2 {
		t.Fatalf("usage=%+v", usage)
	}
	if gotPath != "/algo/api/v2/service/pro/sse/agent_chat_generation" {
		t.Fatalf("path=%q", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer COSY.") {
		t.Fatalf("auth=%q", gotAuth)
	}
	if gotBody["model"] != "auto" {
		t.Fatalf("body=%+v", gotBody)
	}
}

func TestEnvelopeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"statusCodeValue":403,"body":"{\"code\":\"101\",\"message\":\"Signature invalid\"}"}`+"\n\n")
	}))
	defer srv.Close()

	p := newProvider(profile{id: "qoder-test", baseURL: srv.URL, openAPI: srv.URL, cosyVer: "1.1.77"})
	env := provider.NewAccountEnv("acc", "qoder-test")
	env.Credentials = map[string]string{"token": "tk", "user_id": "uid", "machine_id": "mid"}
	env.HTTPClient = srv.Client()

	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "qoder-test/auto", UpstreamModel: "auto", Stream: true,
	})
	if err != nil {
		// 403 在首包前也可能直接返回 API 错误。
		if ae := ir.AsAPIError(err); ae == nil || ae.Code != ir.ErrCodeUpstreamAuth {
			t.Fatalf("err = %v", err)
		}
		return
	}
	var gotErr error
	for ev := range ch {
		if ev.Type == ir.EventError {
			gotErr = ev.Err
		}
	}
	if gotErr == nil {
		t.Fatal("应返回流内错误")
	}
}

func TestParseDesktopAuthJSON(t *testing.T) {
	auth, err := parseDesktopAuthJSON([]byte(`{
	  "token": "dt-test",
	  "refreshToken": "drt-test",
	  "expiresAt": "2030-01-02T03:04:05Z",
	  "user": {"id": "u1", "name": "测试用户", "email": "a@b.c"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if auth.Token != "dt-test" || auth.User.ID != "u1" || auth.User.Name != "测试用户" {
		t.Fatalf("auth = %+v", auth)
	}
	if _, err := parseDesktopAuthJSON([]byte(`{"user":{}}`)); err == nil {
		t.Fatal("空 token 应报错")
	}
}

func TestManifest(t *testing.T) {
	for _, id := range []string{"qoder-cn", "qoder-intl"} {
		item, ok := provider.Get(id)
		if !ok {
			t.Fatalf("%s 未注册", id)
		}
		m := item.Manifest()
		if !m.Capabilities.Chat || !m.Capabilities.LocalImport || !m.Capabilities.Refresh {
			t.Fatalf("%s manifest = %+v", id, m)
		}
		if len(m.Models) < 5 {
			t.Fatalf("%s models = %d", id, len(m.Models))
		}
		// Qoder 上游已支持 DeepSeek V4.1，目录必须跟上，否则 UI/模型清单里看不到。
		seen := map[string]bool{}
		for _, model := range m.Models {
			seen[model.UpstreamModel] = true
		}
		for _, want := range []string{"DeepSeek-V4.1-Flash", "DeepSeek-V4.1-Pro"} {
			if !seen[want] {
				t.Fatalf("%s 缺少模型 %s", id, want)
			}
		}
	}
}

func TestMapFinishReason(t *testing.T) {
	cases := map[string]string{
		"stop": "stop", "length": "length", "tool_calls": "tool_calls",
		"content_filter": "content_filter", "weird": "stop",
	}
	for in, want := range cases {
		if got := mapFinishReason(in); got != want {
			t.Errorf("mapFinishReason(%q)=%q want %q", in, got, want)
		}
	}
}

func TestChatStreamRetriesTransientAllModelsFailed(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		io.Copy(io.Discard, r.Body)
		if hits == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"code":"provider_error","message":"All models failed","type":"provider_error"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"statusCodeValue\":200,\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"ok\\\"}}]}\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"statusCodeValue\":200,\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{},\\\"finish_reason\\\":\\\"stop\\\"}]}\"}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newProvider(profile{
		id: "qoder-test", displayName: "Qoder Test", baseURL: srv.URL,
		openAPI: srv.URL, cosyVer: "1.1.77",
	})
	env := provider.NewAccountEnv("acc-retry", "qoder-test")
	env.Credentials = map[string]string{"token": "tk", "user_id": "u", "machine_id": "m", "user_name": "n"}
	env.HTTPClient = srv.Client()

	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "qoder-test/auto", UpstreamModel: "auto", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("瞬时 429 应被内部重试吞掉: %v", err)
	}
	var text string
	for ev := range ch {
		text += ev.TextDelta
	}
	if hits != 2 {
		t.Fatalf("应重试 1 次（共 2 次请求），实际 %d", hits)
	}
	if text != "ok" {
		t.Fatalf("重试后应拿到内容，得到 %q", text)
	}
}

func TestChatStreamGivesUpAfterRetries(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"code":"provider_error","message":"All models failed"}`)
	}))
	defer srv.Close()

	p := newProvider(profile{id: "qoder-test", displayName: "Qoder Test", baseURL: srv.URL, openAPI: srv.URL, cosyVer: "1.1.77"})
	env := provider.NewAccountEnv("acc-retry2", "qoder-test")
	env.Credentials = map[string]string{"token": "tk", "user_id": "u", "machine_id": "m"}
	env.HTTPClient = srv.Client()

	_, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "qoder-test/auto", UpstreamModel: "auto", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err == nil {
		t.Fatal("重试两次仍失败应返回错误")
	}
	if ae := ir.AsAPIError(err); ae == nil || ae.Code != ir.ErrCodeUpstreamRateLimit {
		t.Fatalf("错误码应为限流: %+v", err)
	}
	if hits != 3 {
		t.Fatalf("应共请求 3 次（1+2 重试），实际 %d", hits)
	}
}
