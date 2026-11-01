package openaicompat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// AWS 官方 SigV4 测试向量（IAM ListUsers）。
func TestSigV4AWSVector(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
	now := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	if err := signAWSV4(req, nil, "AKIDEXAMPLE",
		"wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "", "us-east-1", "iam", now); err != nil {
		t.Fatal(err)
	}
	auth := req.Header.Get("Authorization")
	want := "5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"
	if !strings.HasSuffix(auth, "Signature="+want) {
		t.Fatalf("authorization = %q, want signature %s", auth, want)
	}
	if !strings.Contains(auth, "SignedHeaders=content-type;host;x-amz-date") {
		t.Fatalf("signed headers = %q", auth)
	}
}

func TestBedrockSigV4ChatStream(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"bedrock-ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &Provider{cfg: Config{ID: "bedrock", BaseURL: srv.URL, SigV4Service: "bedrock"}}
	env := provider.NewAccountEnv("acc-bedrock", "bedrock")
	env.Credentials = map[string]string{
		"aws_access_key_id":     "AKIDTEST",
		"aws_secret_access_key": "secret-test",
		"aws_session_token":     "session-test",
		"region":                "us-east-1",
	}
	env.SetHTTPClient(srv.Client())
	req := &ir.UnifiedChatRequest{
		Model: "bedrock/test-model", UpstreamModel: "test-model",
		Stream: true, Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
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
	if text.String() != "bedrock-ok" {
		t.Fatalf("text = %q", text.String())
	}
	if !strings.HasPrefix(got.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKIDTEST/") {
		t.Fatalf("authorization = %q", got.Get("Authorization"))
	}
	if got.Get("X-Amz-Date") == "" || got.Get("X-Amz-Content-Sha256") == "" || got.Get("X-Amz-Security-Token") != "session-test" {
		t.Fatalf("sigv4 headers = %v", got)
	}
	if got.Get("Authorization") == "Bearer " || got.Get("api-key") != "" {
		t.Fatalf("unexpected bearer auth: %v", got)
	}
}

func TestBedrockManifestHasSigV4Fields(t *testing.T) {
	p := &Provider{cfg: Config{ID: "bedrock", BaseURL: "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1", SigV4Service: "bedrock"}}
	keys := map[string]bool{}
	for _, f := range p.Manifest().AuthFormSchema {
		keys[f.Key] = true
	}
	for _, k := range []string{"api_key", "aws_access_key_id", "aws_secret_access_key", "aws_session_token", "region"} {
		if !keys[k] {
			t.Fatalf("missing form field %s: %v", k, keys)
		}
	}
}

func TestAnthropicUpstreamProtocol(t *testing.T) {
	var gotPath, gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`event: message_start\ndata: {"type":"message_start","message":{"id":"m1","usage":{"input_tokens":1,"output_tokens":1}}}`,
			`event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"anthropic-ok"}}`,
			`event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
			`event: message_stop\ndata: {"type":"message_stop"}`,
		}
		for _, f := range frames {
			_, _ = io.WriteString(w, strings.ReplaceAll(f, `\n`, "\n")+"\n\n")
		}
	}))
	defer srv.Close()

	p := &Provider{cfg: Config{ID: "zhipu", BaseURL: "https://chat.invalid", AllowAnthropicUpstream: true}}
	env := provider.NewAccountEnv("acc-anth", "zhipu")
	env.Credentials = map[string]string{
		"base_url": srv.URL, "api_key": "zhipu-key", "api_protocol": "anthropic",
	}
	env.SetHTTPClient(srv.Client())
	req := &ir.UnifiedChatRequest{
		Model: "zhipu/glm-5.3", UpstreamModel: "glm-5.3", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
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
	if text.String() != "anthropic-ok" || gotPath != "/v1/messages" || gotKey != "zhipu-key" || gotAuth != "" {
		t.Fatalf("text=%q path=%q key=%q auth=%q", text.String(), gotPath, gotKey, gotAuth)
	}

	// 没填 Anthropic Base URL 时必须拒绝，而不是误打 Chat 端点。
	env.Credentials["base_url"] = ""
	if _, err := p.ChatStream(context.Background(), env, req); err == nil || !strings.Contains(err.Error(), "Anthropic 端点") {
		t.Fatalf("missing anthropic base err = %v", err)
	}
}

func TestAnthropicProtocolFormFields(t *testing.T) {
	p := &Provider{cfg: Config{ID: "zhipu", AllowAnthropicUpstream: true}}
	keys := map[string]bool{}
	for _, f := range p.Manifest().AuthFormSchema {
		keys[f.Key] = true
	}
	if !keys["api_protocol"] || !keys["api_key_auth"] {
		t.Fatalf("missing protocol fields: %v", keys)
	}
}
