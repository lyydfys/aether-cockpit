package openaicompat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/core/channels"
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestVendorProvidersRegistered(t *testing.T) {
	seen := map[string]bool{}
	for _, v := range channels.Vendors() {
		if seen[v.ID] {
			t.Fatalf("duplicate vendor id %q", v.ID)
		}
		seen[v.ID] = true
		p, ok := provider.Get(v.ID)
		if !ok {
			t.Fatalf("vendor %q not registered", v.ID)
		}
		m := p.Manifest()
		if m.ID != v.ID || m.DisplayName != v.DisplayName {
			t.Errorf("%s: manifest id/name = %q/%q", v.ID, m.ID, m.DisplayName)
		}
		if !m.Capabilities.Chat {
			t.Errorf("%s: manifest lacks Chat capability", v.ID)
		}
		for _, model := range m.Models {
			if !strings.HasPrefix(model.ID, v.ID+"/") {
				t.Errorf("%s: model id %q lacks vendor prefix", v.ID, model.ID)
			}
		}
	}
}

func TestXAIResponsesUpstream(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"xai-resp\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n")
	}))
	defer srv.Close()

	p, ok := provider.Get("xai")
	if !ok {
		t.Fatal("xai not registered")
	}
	hasResponses := false
	for _, f := range p.Manifest().AuthFormSchema {
		if f.Key != "api_protocol" {
			continue
		}
		for _, opt := range f.Options {
			if opt == "responses" {
				hasResponses = true
			}
		}
	}
	if !hasResponses {
		t.Fatal("xai manifest must expose api_protocol=responses")
	}

	env := provider.NewAccountEnv("acc-xai-resp", "xai")
	env.Credentials = map[string]string{
		"base_url": srv.URL + "/v1", "api_key": "xai-key", "api_protocol": "responses",
	}
	env.SetHTTPClient(srv.Client())
	cc, ok := p.(provider.ChatCapability)
	if !ok {
		t.Fatal("xai must implement ChatCapability")
	}
	ch, err := cc.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "xai/grok-4", UpstreamModel: "grok-4", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
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
	if text.String() != "xai-resp" || gotPath != "/v1/responses" || gotAuth != "Bearer xai-key" {
		t.Fatalf("text=%q path=%q auth=%q", text.String(), gotPath, gotAuth)
	}
}

func TestVendorDefaultBaseURL(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p, _ := provider.Get("deepseek")
	env := provider.NewAccountEnv("acc", "deepseek")
	env.Credentials = map[string]string{"api_key": "sk-test"}
	env.SetHTTPClient(srv.Client())
	// 渠道自带默认 Base URL；这里换成假上游，验证默认值参与拼接。
	p.(*Provider).cfg.BaseURL = srv.URL + "/v1"

	ch, err := p.(*Provider).ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model:              "deepseek/deepseek-chat",
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
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth = %q", gotAuth)
	}
	if text.String() != "ok" {
		t.Errorf("text = %q", text.String())
	}
}

func TestLocalVendorNeedsNoKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("local vendor should not send Authorization")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"local\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p, _ := provider.Get("ollama")
	env := provider.NewAccountEnv("acc", "ollama")
	env.Credentials = map[string]string{"base_url": srv.URL + "/v1"}
	env.SetHTTPClient(srv.Client())
	ch, err := p.(*Provider).ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model:              "ollama/llama3.2",
		DownstreamProtocol: ir.ProtocolOpenAIChat,
		Items:              []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
		Stream:             true,
	})
	if err != nil {
		t.Fatalf("ChatStream without key: %v", err)
	}
	got := ""
	for ev := range ch {
		if ev.Type == ir.EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
		if ev.Type == ir.EventTextDelta {
			got += ev.TextDelta
		}
	}
	if got != "local" {
		t.Fatalf("text = %q", got)
	}
}
