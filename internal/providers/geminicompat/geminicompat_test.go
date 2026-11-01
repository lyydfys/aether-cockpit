package geminicompat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestChatStreamNativeGemini(t *testing.T) {
	var gotPath, gotKey, gotAlt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		gotAlt = r.URL.Query().Get("alt")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" world\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":3,\"cachedContentTokenCount\":1,\"candidatesTokenCount\":2,\"totalTokenCount\":5}}\n\n")
	}))
	defer srv.Close()

	base, _ := provider.Get("gemini-compat")
	p := base.(provider.ChatCapability)
	env := provider.NewAccountEnv("acc", "gemini-compat")
	env.Credentials = map[string]string{"base_url": srv.URL, "api_key": "k-123"}
	env.SetHTTPClient(srv.Client())
	req := &ir.UnifiedChatRequest{
		Model:              "gemini-compat/gemini-2.5-pro",
		DownstreamProtocol: ir.ProtocolOpenAIChat,
		Items:              []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
		Stream:             true,
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	var usage *ir.Usage
	finish := ""
	for ev := range ch {
		switch ev.Type {
		case ir.EventError:
			t.Fatalf("stream error: %v", ev.Err)
		case ir.EventTextDelta:
			text.WriteString(ev.TextDelta)
		case ir.EventUsage:
			usage = ev.Usage
		case ir.EventDone:
			finish = ev.FinishReason
		}
	}
	if gotPath != "/v1beta/models/gemini-2.5-pro:streamGenerateContent" {
		t.Errorf("path = %q", gotPath)
	}
	if gotKey != "k-123" || gotAlt != "sse" {
		t.Errorf("key=%q alt=%q", gotKey, gotAlt)
	}
	if text.String() != "hello world" {
		t.Errorf("text = %q", text.String())
	}
	if usage == nil || usage.InputTokens != 2 || usage.CachedTokens != 1 ||
		usage.OutputTokens != 2 || usage.TotalTokens != 5 {
		t.Errorf("usage = %+v", usage)
	}
	if finish != "stop" {
		t.Errorf("finish = %q", finish)
	}
}

func TestChatStreamRequiresKey(t *testing.T) {
	base, _ := provider.Get("gemini-compat")
	p := base.(provider.ChatCapability)
	env := provider.NewAccountEnv("acc", "gemini-compat")
	env.Credentials = map[string]string{"base_url": "http://127.0.0.1:1"}
	if _, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{Model: "gemini-compat/x"}); err == nil {
		t.Fatal("缺少 API Key 时应报错")
	}
}
