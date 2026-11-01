package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func responsesEnv(baseURL string) *provider.AccountEnv {
	env := provider.NewAccountEnv("acc-resp", "vendor")
	env.Credentials = map[string]string{"base_url": baseURL, "api_key": "resp-key", "api_protocol": "responses"}
	return env
}

func TestResponsesUpstreamTextAndUsage(t *testing.T) {
	var gotPath, gotAuth string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			"event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"think\"}\n\n",
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"resp-ok\"}\n\n",
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15,\"input_tokens_details\":{\"cached_tokens\":2},\"output_tokens_details\":{\"reasoning_tokens\":1}}}}\n\n",
		}
		for _, f := range frames {
			_, _ = io.WriteString(w, f)
		}
	}))
	defer srv.Close()

	p := &Provider{cfg: Config{ID: "vendor", AllowResponsesUpstream: true}}
	max := 64
	req := &ir.UnifiedChatRequest{
		Model: "vendor/gpt-5", UpstreamModel: "gpt-5", Stream: true,
		Instructions: "be brief", MaxOutputTokens: &max,
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "system", Text: "sys"},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
		},
		Tools: []ir.UnifiedToolSpec{{Name: "ping", Description: "ping", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
	ch, err := p.ChatStream(context.Background(), responsesEnv(srv.URL), req)
	if err != nil {
		t.Fatal(err)
	}
	var text, reasoning string
	var usage *ir.Usage
	finish := ""
	for ev := range ch {
		if ev.Type == ir.EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
		switch ev.Type {
		case ir.EventTextDelta:
			text += ev.TextDelta
		case ir.EventReasoningDelta:
			reasoning += ev.TextDelta
		case ir.EventUsage:
			usage = ev.Usage
		case ir.EventDone:
			finish = ev.FinishReason
		}
	}
	if gotPath != "/responses" || gotAuth != "Bearer resp-key" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if body["model"] != "gpt-5" || body["instructions"] != "be brief\n\nsys" || body["max_output_tokens"] != float64(64) || body["stream"] != true {
		t.Fatalf("body = %+v", body)
	}
	if _, ok := body["tools"].([]any); !ok {
		t.Fatalf("tools = %+v", body["tools"])
	}
	if text != "resp-ok" || reasoning != "think" || finish != "stop" {
		t.Fatalf("text=%q reasoning=%q finish=%q", text, reasoning, finish)
	}
	if usage == nil || usage.InputTokens != 8 || usage.CachedTokens != 2 || usage.OutputTokens != 5 || usage.ReasoningTokens != 1 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestResponsesUpstreamToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"get_weather\"}}\n\n",
			"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item_1\",\"delta\":\"{\\\"city\\\":\\\"SZ\\\"}\"}\n\n",
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}}\n\n",
		}
		for _, f := range frames {
			_, _ = io.WriteString(w, f)
		}
	}))
	defer srv.Close()
	p := &Provider{cfg: Config{ID: "vendor", AllowResponsesUpstream: true}}
	ch, err := p.ChatStream(context.Background(), responsesEnv(srv.URL), &ir.UnifiedChatRequest{
		Model: "vendor/gpt-5", UpstreamModel: "gpt-5", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "weather"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var toolName, toolID, args, finish string
	for ev := range ch {
		switch ev.Type {
		case ir.EventError:
			t.Fatalf("error: %v", ev.Err)
		case ir.EventToolCallStart:
			toolName, toolID = ev.ToolName, ev.ToolCallID
		case ir.EventToolCallDelta:
			args += ev.ArgsDelta
		case ir.EventDone:
			finish = ev.FinishReason
		}
	}
	if toolName != "get_weather" || toolID != "call_1" || args != `{"city":"SZ"}` || finish != "tool_calls" {
		t.Fatalf("tool=%q id=%q args=%q finish=%q", toolName, toolID, args, finish)
	}
}

func TestResponsesProtocolField(t *testing.T) {
	p := &Provider{cfg: Config{ID: "vendor", AllowResponsesUpstream: true}}
	var opts []string
	for _, f := range p.Manifest().AuthFormSchema {
		if f.Key == "api_protocol" {
			opts = f.Options
		}
	}
	found := false
	for _, o := range opts {
		if o == "responses" {
			found = true
		}
	}
	if !found {
		t.Fatalf("api_protocol options = %v", opts)
	}
}
