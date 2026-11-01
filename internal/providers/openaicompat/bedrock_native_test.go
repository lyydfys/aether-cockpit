package openaicompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
)

// bedrockFrame 构造一条 invoke-with-response-stream 的 chunk 帧。
func bedrockFrame(t *testing.T, event string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"bytes": base64.StdEncoding.EncodeToString([]byte(event)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return wirecodec.EncodeAWSMessage(
		map[string]string{":event-type": "chunk", ":message-type": "event", ":content-type": "application/json"},
		body,
	)
}

func TestBedrockClaudeNativeStream(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		events := []string{
			`{"type":"message_start","message":{"id":"msg_bedrock","usage":{"input_tokens":12,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"bedrock-native"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":9}}`,
			`{"type":"message_stop"}`,
		}
		for _, ev := range events {
			_, _ = w.Write(bedrockFrame(t, ev))
		}
	}))
	defer srv.Close()

	p := &Provider{cfg: Config{ID: "bedrock", BaseURL: srv.URL + "/openai/v1", SigV4Service: "bedrock"}}
	env := provider.NewAccountEnv("acc-bedrock-native", "bedrock")
	env.Credentials = map[string]string{
		"base_url":              srv.URL + "/openai/v1",
		"aws_access_key_id":     "AKIDTEST",
		"aws_secret_access_key": "secret-test",
		"region":                "us-west-2",
	}
	env.SetHTTPClient(srv.Client())
	req := &ir.UnifiedChatRequest{
		Model:         "bedrock/us.anthropic.claude-sonnet-4-5-20250929-v1:0",
		UpstreamModel: "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
		Stream:        true,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
		Tools: []ir.UnifiedToolSpec{{
			Name: "read_file", Description: "read",
			Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
	}

	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}
	var text, toolArgs strings.Builder
	var finish, toolName, callID string
	sawUsage := false
	for ev := range ch {
		switch ev.Type {
		case ir.EventError:
			t.Fatalf("stream error: %v", ev.Err)
		case ir.EventTextDelta:
			text.WriteString(ev.TextDelta)
		case ir.EventToolCallStart:
			toolName, callID = ev.ToolName, ev.ToolCallID
		case ir.EventToolCallDelta:
			toolArgs.WriteString(ev.ArgsDelta)
		case ir.EventUsage:
			if ev.Usage != nil && ev.Usage.InputTokens == 12 {
				sawUsage = true
			}
		case ir.EventDone:
			finish = ev.FinishReason
		}
	}
	if text.String() != "bedrock-native" || finish != "tool_calls" {
		t.Fatalf("text=%q finish=%q", text.String(), finish)
	}
	if toolName != "read_file" || callID != "toolu_1" || toolArgs.String() != `{"path":"a.txt"}` {
		t.Fatalf("tool name=%q id=%q args=%q", toolName, callID, toolArgs.String())
	}
	if !sawUsage {
		t.Fatal("missing usage from message_start")
	}
	if gotPath != "/model/us.anthropic.claude-sonnet-4-5-20250929-v1:0/invoke-with-response-stream" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 Credential=AKIDTEST/") {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotBody["anthropic_version"] != "bedrock-2023-05-31" {
		t.Fatalf("anthropic_version = %v", gotBody["anthropic_version"])
	}
	if _, ok := gotBody["model"]; ok {
		t.Fatalf("body must not contain model: %v", gotBody)
	}
	if _, ok := gotBody["stream"]; ok {
		t.Fatalf("body must not contain stream: %v", gotBody)
	}
	if _, ok := gotBody["tools"]; !ok {
		t.Fatalf("body missing tools: %v", gotBody)
	}
}

func TestBedrockClaudeNativeJSON(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_json","type":"message","content":[{"type":"text","text":"bedrock-json"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`)
	}))
	defer srv.Close()

	p := &Provider{cfg: Config{ID: "bedrock", SigV4Service: "bedrock"}}
	env := provider.NewAccountEnv("acc-bedrock-json", "bedrock")
	env.Credentials = map[string]string{
		"base_url": srv.URL + "/openai/v1",
		"api_key":  "bedrock-api-key",
		"region":   "us-east-1",
	}
	env.SetHTTPClient(srv.Client())
	req := &ir.UnifiedChatRequest{
		Model:         "bedrock/anthropic.claude-3-5-haiku-20241022-v1:0",
		UpstreamModel: "anthropic.claude-3-5-haiku-20241022-v1:0",
		Stream:        false,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}
	var text, finish string
	for ev := range ch {
		if ev.Type == ir.EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
		if ev.Type == ir.EventTextDelta {
			text += ev.TextDelta
		}
		if ev.Type == ir.EventDone {
			finish = ev.FinishReason
		}
	}
	if text != "bedrock-json" || finish != "end_turn" && finish != "stop" {
		t.Fatalf("text=%q finish=%q", text, finish)
	}
	if gotPath != "/model/anthropic.claude-3-5-haiku-20241022-v1:0/invoke" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestBedrockClaudeModelDetection(t *testing.T) {
	for _, m := range []string{
		"anthropic.claude-3-5-sonnet-20241022-v2:0",
		"us.anthropic.claude-sonnet-4-5-20250929-v1:0",
		"claude-3-opus",
	} {
		if !isBedrockClaudeModel(m) {
			t.Fatalf("%s should be native Claude", m)
		}
	}
	for _, m := range []string{"deepseek.v3.2", "openai.gpt-oss-120b-1:0", "qwen.qwen3-coder-480b-a35b-v1:0"} {
		if isBedrockClaudeModel(m) {
			t.Fatalf("%s should stay OpenAI-compatible", m)
		}
	}
}

func TestBedrockRootURL(t *testing.T) {
	cases := map[string]string{
		"https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1": "https://bedrock-runtime.us-east-1.amazonaws.com",
		"https://bedrock-runtime.eu-west-1.amazonaws.com/openai":    "https://bedrock-runtime.eu-west-1.amazonaws.com",
		"https://proxy.example.com/bedrock/openai/v1/":              "https://proxy.example.com/bedrock",
		"https://proxy.example.com/bedrock":                         "https://proxy.example.com/bedrock",
	}
	for in, want := range cases {
		got, err := bedrockRootURL(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != want {
			t.Fatalf("%s => %s, want %s", in, got, want)
		}
	}
	if _, err := bedrockRootURL("not-a-url"); err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

func TestBedrockClaudeExceptionFrame(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		payload := []byte(`{"message":"The security token included in the request is invalid."}`)
		_, _ = w.Write(wirecodec.EncodeAWSMessage(
			map[string]string{":message-type": "exception", ":exception-type": "UnrecognizedClientException"},
			payload,
		))
	}))
	defer srv.Close()

	p := &Provider{cfg: Config{ID: "bedrock", SigV4Service: "bedrock"}}
	env := provider.NewAccountEnv("acc-bedrock-err", "bedrock")
	env.Credentials = map[string]string{
		"base_url": srv.URL + "/openai/v1",
		"api_key":  "k",
	}
	env.SetHTTPClient(srv.Client())
	req := &ir.UnifiedChatRequest{
		Model: "bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0", UpstreamModel: "anthropic.claude-3-5-sonnet-20241022-v2:0",
		Stream: true, Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}
	var errText string
	for ev := range ch {
		if ev.Type == ir.EventError && ev.Err != nil {
			errText = ev.Err.Error()
		}
	}
	if !strings.Contains(errText, "UnrecognizedClientException") {
		t.Fatalf("error = %q", errText)
	}
}
