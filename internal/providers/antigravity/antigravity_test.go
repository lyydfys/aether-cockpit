package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestBuildRequestBody(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Instructions: "你是助手",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "你好"},
			{Type: ir.ItemTypeReasoning, Text: "先想一下", ThoughtSignature: "sig-1"},
			{Type: ir.ItemTypeFunctionCall, CallID: "call-1", Name: "get_weather", Arguments: `{"city":"SZ"}`},
			{Type: ir.ItemTypeFunctionCallOutput, CallID: "call-1", Output: "晴"},
		},
		Tools: []ir.UnifiedToolSpec{{
			Name:        "get_weather",
			Description: "查询天气",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string","title":"城市"}},"required":["city"],"additionalProperties":false}`),
		}},
	}

	body := buildRequestBody(req, "gemini-3-flash", "proj-1")
	if body["model"] != "gemini-3-flash" || body["project"] != "proj-1" {
		t.Fatalf("envelope = %+v", body)
	}
	if body["userAgent"] != "antigravity" || body["requestType"] != "agent" {
		t.Fatalf("envelope = %+v", body)
	}
	requestID, _ := body["requestId"].(string)
	if !strings.HasPrefix(requestID, "agent-") {
		t.Fatalf("requestId = %q", requestID)
	}

	inner, _ := body["request"].(map[string]any)
	if inner == nil {
		t.Fatal("request 为空")
	}
	if sid, _ := inner["sessionId"].(string); !strings.HasPrefix(sid, "-") {
		t.Fatalf("sessionId = %q", sid)
	}
	if si, _ := inner["systemInstruction"].(map[string]any); si == nil {
		t.Fatal("systemInstruction 缺失")
	}

	contents, _ := inner["contents"].([]any)
	if len(contents) < 2 {
		t.Fatalf("contents = %+v", contents)
	}
	first, _ := contents[0].(map[string]any)
	if first["role"] != "user" {
		t.Fatalf("首条 role = %v", first["role"])
	}

	tools, _ := inner["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %+v", tools)
	}
	group, _ := tools[0].(map[string]any)
	decls, _ := group["functionDeclarations"].([]any)
	if len(decls) != 1 {
		t.Fatalf("declarations = %+v", decls)
	}
	decl, _ := decls[0].(map[string]any)
	params, _ := decl["parameters"].(map[string]any)
	if _, exists := params["additionalProperties"]; exists {
		t.Fatal("additionalProperties 应被清洗")
	}
	props, _ := params["properties"].(map[string]any)
	city, _ := props["city"].(map[string]any)
	if _, exists := city["title"]; exists {
		t.Fatal("title 应被清洗")
	}
	if city["type"] != "string" {
		t.Fatalf("city schema = %+v", city)
	}
}

func TestChatStreamParsesGeminiSSE(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "text/event-stream")
		frames := []string{
			`{"response":{"candidates":[{"content":{"parts":[{"text":"你"}]}}]}}`,
			`{"response":{"candidates":[{"content":{"parts":[{"text":"好","thought":true,"thoughtSignature":"sig-x"}]}}]}}`,
			`{"response":{"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"city":"SZ"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":4,"totalTokenCount":7,"thoughtsTokenCount":2}}}`,
		}
		for _, f := range frames {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", f)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &Provider{}
	env := provider.NewAccountEnv("acc-1", "antigravity")
	env.Credentials = map[string]string{
		"access_token": "tk",
		"expires_at":   time.Now().Add(2 * time.Hour).Format(time.RFC3339),
		"project_id":   "proj-1",
		"base_url":     srv.URL,
	}
	env.HTTPClient = srv.Client()

	req := &ir.UnifiedChatRequest{
		Model:         "antigravity/gemini-3-flash",
		UpstreamModel: "gemini-3-flash",
		Stream:        true,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}

	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}

	var (
		text, reasoning, finish string
		toolName, toolArgs      string
		usage                   *ir.Usage
	)
	for ev := range ch {
		switch ev.Type {
		case ir.EventTextDelta:
			text += ev.TextDelta
		case ir.EventReasoningDelta:
			reasoning += ev.TextDelta
		case ir.EventToolCallStart:
			toolName = ev.ToolName
		case ir.EventToolCallDelta:
			toolArgs += ev.ArgsDelta
		case ir.EventUsage:
			usage = ev.Usage
		case ir.EventDone:
			finish = ev.FinishReason
		}
	}

	if text != "你" {
		t.Fatalf("text = %q", text)
	}
	if reasoning != "好" {
		t.Fatalf("reasoning = %q", reasoning)
	}
	if toolName != "get_weather" || toolArgs != `{"city":"SZ"}` {
		t.Fatalf("tool = %q %q", toolName, toolArgs)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish = %q", finish)
	}
	if usage == nil || usage.InputTokens != 3 || usage.OutputTokens != 4 || usage.TotalTokens != 7 || usage.ReasoningTokens != 2 {
		t.Fatalf("usage = %+v", usage)
	}

	if gotPath != "/v1internal:streamGenerateContent" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "alt=sse") {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotAuth != "Bearer tk" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotBody["model"] != "gemini-3-flash" || gotBody["project"] != "proj-1" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestChatStreamMapsLocationError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":400,"message":"User location is not supported for the API use.","status":"FAILED_PRECONDITION"}}`)
	}))
	defer srv.Close()

	p := &Provider{}
	env := provider.NewAccountEnv("acc-2", "antigravity")
	env.Credentials = map[string]string{
		"access_token": "tk",
		"expires_at":   time.Now().Add(2 * time.Hour).Format(time.RFC3339),
		"project_id":   "proj-1",
		"base_url":     srv.URL,
	}
	env.HTTPClient = srv.Client()

	_, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "antigravity/gemini-3-flash", UpstreamModel: "gemini-3-flash", Stream: true,
	})
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeBadRequest {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(ae.Message, "地区") {
		t.Fatalf("message = %s", ae.Message)
	}
}

func TestMapFinishReason(t *testing.T) {
	cases := map[string]string{
		"STOP":        "stop",
		"MAX_TOKENS":  "length",
		"SAFETY":      "content_filter",
		"RECITATION":  "content_filter",
		"UNKNOWN_XYZ": "stop",
	}
	for in, want := range cases {
		if got := mapFinishReason(in); got != want {
			t.Errorf("mapFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseLocalCredential(t *testing.T) {
	raw := []byte(`{
	  "token": {
	    "access_token": "ya29.test",
	    "refresh_token": "1//refresh",
	    "token_type": "Bearer",
	    "expiry": "2030-01-02T03:04:05.1234567+08:00"
	  },
	  "auth_method": "consumer"
	}`)
	draft := parseLocalCredential(raw)
	if draft == nil {
		t.Fatal("解析失败")
	}
	if draft.Credentials["access_token"] != "ya29.test" || draft.Credentials["refresh_token"] != "1//refresh" {
		t.Fatalf("credentials = %+v", draft.Credentials)
	}
	if draft.ExpiresAt == 0 {
		t.Fatal("expires_at 未解析")
	}
	if draft.Label == "" || !strings.HasPrefix(draft.Source, "local_import:") {
		t.Fatalf("draft = %+v", draft)
	}
	if parseLocalCredential([]byte(`{"token":{}}`)) != nil {
		t.Fatal("空 token 应返回 nil")
	}
}

func TestManifest(t *testing.T) {
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID", "test-client-id")
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "test-client-secret")
	m := (&Provider{}).Manifest()
	if m.ID != "antigravity" || !m.Capabilities.Chat || !m.Capabilities.LocalImport || !m.Capabilities.Refresh {
		t.Fatalf("manifest = %+v", m)
	}
	if len(m.Models) < 10 {
		t.Fatalf("models = %d", len(m.Models))
	}
	if m.OAuth == nil || m.OAuth.ClientID == "" {
		t.Fatal("OAuth 未声明")
	}
	found := false
	for _, model := range m.Models {
		if model.ID == "antigravity/claude-sonnet-4-6" {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺少 claude-sonnet-4-6: %+v", m.Models)
	}
}

func TestManifestOmitsOAuthWithoutClientCredentials(t *testing.T) {
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID", "")
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "")
	m := (&Provider{}).Manifest()
	if m.OAuth != nil {
		t.Fatal("OAuth config should be omitted without local client credentials")
	}
	for _, auth := range m.SupportedAuths {
		if auth == provider.AuthOAuthPKCE {
			t.Fatal("OAuth PKCE should not be advertised without local client credentials")
		}
	}
}

func TestGeminiStructuredControls(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		ResponseFormat: &ir.ResponseFormat{
			Type: "json_schema", Schema: []byte(`{"type":"object","properties":{"a":{"type":"string"}}}`),
		},
		ToolChoice: &ir.ToolChoice{Mode: "function", Name: "read"},
	}
	gc := generationConfig(req, "gemini")
	if gc["responseMimeType"] != "application/json" || gc["responseSchema"] == nil {
		t.Fatalf("generationConfig = %v", gc)
	}
	cfg := toolConfig(req)
	fcc, _ := cfg["functionCallingConfig"].(map[string]any)
	names, _ := fcc["allowedFunctionNames"].([]string)
	if fcc["mode"] != "ANY" || len(names) != 1 || names[0] != "read" {
		t.Fatalf("toolConfig = %v", cfg)
	}
}
