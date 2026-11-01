package claude

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 指纹向量来自 relay-panel 对 Node.js crypto 的固定输出，验证 UTF-16 语义。
func TestClaudeCodeFingerprintVectors(t *testing.T) {
	const version = "2.1.283"
	cases := []struct {
		text string
		want string
	}{
		{"", "49b"},
		{"hi", "49b"},
		{"abcdefghijklmnopqrstuvwxyz", "2e9"},
		{"这是一个用于验证中文指纹计算的测试消息，请保持一致。", "c7e"},
		{"你好世界呀", "4ed"},
		{"abcd中fg文ijklmnopqrst界", "183"},
		{"😀abcdefghijklmnopqrstuvwxyz", "c16"},
		{"abcd😀fghijklmnopqrstuvwxyz", "d96"},
		{"abc😀efghijklmnopqrstuvwxyz", "d96"},
		{"abcd😀😀efghijklmnopqrstuvwxyz", "0e9"},
		{"abcd\x00fg\x00hijklmnopqrs\x00", "03e"},
	}
	for _, tc := range cases {
		for _, blocks := range []bool{false, true} {
			var content any = tc.text
			if blocks {
				content = []any{map[string]any{"type": "text", "text": tc.text}}
			}
			body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": content}}}
			if got := claudeCodeFingerprint(body, version); got != tc.want {
				t.Errorf("text=%q blocks=%v fingerprint=%s want %s", tc.text, blocks, got, tc.want)
			}
		}
	}
}

func TestClaudeBillingInjection(t *testing.T) {
	body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	if !injectClaudeBillingHeader(body, "claude-cli/2.1.258 (external, cli)") {
		t.Fatal("Claude CLI UA should inject billing")
	}
	system, _ := body["system"].([]any)
	if len(system) != 1 {
		t.Fatalf("system = %+v", system)
	}
	block, _ := system[0].(map[string]any)
	text, _ := block["text"].(string)
	want := "x-anthropic-billing-header: cc_version=2.1.258." + claudeCodeFingerprint(body, "2.1.258") + "; cc_entrypoint=cli;"
	if text != want {
		t.Fatalf("billing = %q want %q", text, want)
	}
	if injectClaudeBillingHeader(map[string]any{}, "my-relay/1.0") {
		t.Fatal("non-CLI UA should not inject billing")
	}
}

func TestClaudeOAuthMimicHeadersAndBody(t *testing.T) {
	var got http.Header
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	p := &Provider{}
	env := provider.NewAccountEnv("acc-mimic", channelID)
	env.Credentials = map[string]string{"access_token": "tk", "base_url": srv.URL}
	env.HTTPClient = srv.Client()
	req := &ir.UnifiedChatRequest{
		Model: channelID + "/claude-sonnet-4-6", UpstreamModel: "claude-sonnet-4-6",
		Stream: true, Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if got.Get("X-App") != "cli" || got.Get("X-Stainless-Lang") != "js" || got.Get("X-Stainless-Helper-Method") != "stream" {
		t.Fatalf("mimic headers = %v", got)
	}
	if got.Get("Accept") != "text/event-stream" {
		t.Fatalf("accept = %q", got.Get("Accept"))
	}
	if !strings.Contains(got.Get("User-Agent"), "claude-cli/") {
		t.Fatalf("ua = %q", got.Get("User-Agent"))
	}
	system, _ := body["system"].([]any)
	if len(system) == 0 {
		t.Fatal("missing billing system block")
	}
	block, _ := system[0].(map[string]any)
	text, _ := block["text"].(string)
	if !strings.HasPrefix(text, "x-anthropic-billing-header: cc_version=") || !strings.HasSuffix(text, "; cc_entrypoint=cli;") {
		t.Fatalf("billing = %q", text)
	}
}

func TestClaudeAPIKeyDoesNotMimic(t *testing.T) {
	var got http.Header
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()
	p := &Provider{}
	env := provider.NewAccountEnv("acc-key-mimic", channelID)
	env.Credentials = map[string]string{"api_key": "sk-ant-test", "base_url": srv.URL}
	env.HTTPClient = srv.Client()
	req := &ir.UnifiedChatRequest{
		Model: channelID + "/claude-sonnet-4-6", UpstreamModel: "claude-sonnet-4-6",
		Stream: true, Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if got.Get("X-App") != "" || got.Get("X-Stainless-Lang") != "" {
		t.Fatalf("API key mode should not mimic: %v", got)
	}
	if _, ok := body["system"]; ok {
		t.Fatalf("API key mode should not inject billing: %+v", body["system"])
	}
}

func TestClaudeAPIKeyBearerAuth(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()
	p := &Provider{}
	env := provider.NewAccountEnv("acc-bearer", channelID)
	env.Credentials = map[string]string{
		"api_key": "vendor-key", "api_key_auth": "bearer", "base_url": srv.URL,
	}
	env.HTTPClient = srv.Client()
	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: channelID + "/glm-5.3", UpstreamModel: "glm-5.3", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if got.Get("Authorization") != "Bearer vendor-key" || got.Get("x-api-key") != "" {
		t.Fatalf("headers = %v", got)
	}
}
