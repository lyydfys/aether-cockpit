package githubcopilot

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestChatStreamExchangesTokenAndStreams(t *testing.T) {
	var tokenCalls int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenCalls, 1)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "token ") {
			t.Errorf("token auth = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Copilot-Integration-Id") != "vscode-chat" {
			t.Errorf("token integration id = %q", r.Header.Get("Copilot-Integration-Id"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"token":"copilot-token","expires_at":%d}`, time.Now().Add(time.Hour).Unix())
	}))
	defer tokenSrv.Close()
	oldToken := copilotTokenURL
	copilotTokenURL = tokenSrv.URL
	defer func() { copilotTokenURL = oldToken }()

	chatSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer copilot-token" {
			t.Errorf("chat auth = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Copilot-Integration-Id") != "vscode-chat" || r.Header.Get("Editor-Version") == "" {
			t.Errorf("chat headers = %v", r.Header)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer chatSrv.Close()

	env := provider.NewAccountEnv("copilot-test", channelID)
	env.Credentials = map[string]string{"github_token": "gh-token", "base_url": chatSrv.URL}
	env.HTTPClient = &http.Client{}
	req := &ir.UnifiedChatRequest{
		Model:         "github-copilot/gpt-4o",
		UpstreamModel: "gpt-4o",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := (&Provider{}).ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	var done bool
	for ev := range ch {
		if ev.TextDelta != "" && ev.Type == ir.EventTextDelta {
			text.WriteString(ev.TextDelta)
		}
		if ev.Type == ir.EventDone {
			done = true
		}
	}
	if !strings.Contains(text.String(), "OK") || !done {
		t.Fatalf("text=%q done=%v", text.String(), done)
	}
	if atomic.LoadInt32(&tokenCalls) != 1 {
		t.Fatalf("token calls = %d, want 1", tokenCalls)
	}
	// 第二次对话不应重复交换。
	ch, err = (&Provider{}).ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatalf("ChatStream 2: %v", err)
	}
	for range ch {
	}
	if atomic.LoadInt32(&tokenCalls) != 1 {
		t.Fatalf("token 被重复交换: %d", tokenCalls)
	}
}
