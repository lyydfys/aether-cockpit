package openaicompat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 入站是 Anthropic、且账号配了 anthropic 端点时，必须自动走 /v1/messages。
//
// 事故背景：用户在渠道编辑器里填好 Anthropic 端点，用 Claude Code 连网关，
// 但账号没有 api_protocol 凭证 → 请求被翻译成 chat 打到
// <chat端点>/chat/completions → 对端 404 page not found。
func TestAnthropicInboundAutoRoutesToAnthropicEndpoint(t *testing.T) {
	var hits int32
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotPath = r.URL.Path
		// 只有 /v1/messages 存在；其它路径一律 404（模拟真实中转站）。
		if r.URL.Path != "/v1/messages" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("404 page not found\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"c",
			"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	p := &Provider{cfg: Config{
		ID:                     "open-design",
		BaseURL:                upstream.URL + "/v1",
		AllowAnthropicUpstream: true,
	}}
	env := newTestEnv(t, map[string]string{
		"api_key": "sk-ant-test",
		// chat 端点带 /v1，anthropic 端点不带——正是用户会填的样子。
		"base_url":           upstream.URL + "/v1",
		"chat_base_url":      upstream.URL + "/v1",
		"anthropic_base_url": upstream.URL,
	})

	req := &ir.UnifiedChatRequest{
		DownstreamProtocol: ir.ProtocolAnthropic,
		Model:              "open-design/claude-haiku-5.5",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
		},
	}
	ch, err := p.ChatStream(t.Context(), env, req)
	if err != nil {
		t.Fatalf("ChatStream: %v（上游路径 = %q）", err, gotPath)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("上游收到路径 %q，应是 /v1/messages（自动对齐协议失败）", gotPath)
	}
	// 把流读完，确保没有事件级错误。
	for ev := range ch {
		if ev.Type == ir.EventError && ev.Err != nil {
			t.Fatalf("流内错误: %v", ev.Err)
		}
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("上游没被打到")
	}
}

// 没配 anthropic 端点的账号不应被自动改写行为（保持走 chat）。
func TestInboundAnthropicWithoutEndpointKeepsChat(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	p := &Provider{cfg: Config{
		ID:                     "open-design",
		BaseURL:                upstream.URL,
		AllowAnthropicUpstream: true,
	}}
	env := newTestEnv(t, map[string]string{
		"api_key":  "sk-test",
		"base_url": upstream.URL,
		// 故意不设 anthropic_base_url
	})
	req := &ir.UnifiedChatRequest{
		DownstreamProtocol: ir.ProtocolAnthropic,
		Model:              "open-design/gpt-4o",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
		},
	}
	ch, err := p.ChatStream(t.Context(), env, req)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	for range ch {
	}
	if !strings.HasSuffix(gotPath, "/chat/completions") {
		t.Fatalf("没配 anthropic 端点时应仍走 chat，实际 %q", gotPath)
	}
}
