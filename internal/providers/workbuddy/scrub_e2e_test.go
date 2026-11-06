package workbuddy

import (
	"context"
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

// 端到端：开启清洗后，上游收到的 system 里不应再出现别家客户端指纹。
func TestChatStreamScrubsClaudeFingerprint(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	run := func(t *testing.T, sanitize bool) map[string]any {
		t.Helper()
		gotBody = nil
		p := newProvider(cnProfile)
		env := provider.NewAccountEnv("acc-scrub", cnProfile.id)
		creds := map[string]string{"token": "tk", "user_id": "uid-1", "base_url": srv.URL}
		if sanitize {
			creds["sanitize_claude_prompt"] = "1"
		}
		env.Credentials = creds
		env.HTTPClient = srv.Client()

		req := &ir.UnifiedChatRequest{
			Model:         cnProfile.id + "/glm-5",
			UpstreamModel: "glm-5",
			Instructions:  "You are Claude Code, Anthropic's official CLI for Claude.",
			Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
		}
		ch, err := p.ChatStream(context.Background(), env, req)
		if err != nil {
			t.Fatalf("ChatStream: %v", err)
		}
		for range ch {
		}
		if gotBody == nil {
			t.Fatal("未捕获请求体")
		}
		return gotBody
	}

	// 默认关闭：原样发出。
	raw := run(t, false)
	if sys := firstSystem(raw); !strings.Contains(sys, "Claude Code") {
		t.Fatalf("默认不应改动，system=%q", sys)
	}

	// 开启后：指纹被替换，用户消息不动。
	clean := run(t, true)
	sys := firstSystem(clean)
	if strings.Contains(sys, "Claude Code") || strings.Contains(sys, "Anthropic") {
		t.Fatalf("system 指纹未清洗：%q", sys)
	}
	if !strings.Contains(sys, "helpful AI coding assistant") {
		t.Fatalf("system 未替换为中性描述：%q", sys)
	}
}

func firstSystem(body map[string]any) string {
	msgs, _ := body["messages"].([]any)
	for _, raw := range msgs {
		m, _ := raw.(map[string]any)
		if m != nil && m["role"] == "system" {
			s, _ := m["content"].(string)
			return s
		}
	}
	return ""
}
