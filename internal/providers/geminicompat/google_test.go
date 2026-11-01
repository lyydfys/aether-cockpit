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

func TestGoogleProviderUsesNativeGemini(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"google-ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer srv.Close()

	base, ok := provider.Get("google")
	if !ok {
		t.Fatal("google provider not registered")
	}
	if m := base.Manifest(); m.ID != "google" || m.DisplayName != "Google Gemini" {
		t.Fatalf("manifest = %q/%q", m.ID, m.DisplayName)
	}
	p := base.(provider.ChatCapability)
	env := provider.NewAccountEnv("acc", "google")
	env.Credentials = map[string]string{"base_url": srv.URL, "api_key": "g-key"}
	env.SetHTTPClient(srv.Client())

	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model:              "google/gemini-2.5-pro",
		DownstreamProtocol: ir.ProtocolOpenAIChat,
		Items:              []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
		Stream:             true,
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var text strings.Builder
	for ev := range ch {
		if ev.Type == ir.EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
		if ev.Type == ir.EventTextDelta {
			text.WriteString(ev.TextDelta)
		}
	}
	if gotPath != "/v1beta/models/gemini-2.5-pro:streamGenerateContent" {
		t.Errorf("path = %q", gotPath)
	}
	if gotKey != "g-key" {
		t.Errorf("key = %q", gotKey)
	}
	if text.String() != "google-ok" {
		t.Errorf("text = %q", text.String())
	}
}
