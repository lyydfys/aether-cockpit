package kiro

import (
	"context"
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

func TestManifest(t *testing.T) {
	p, ok := provider.Get("kiro")
	if !ok {
		t.Fatal("kiro 未注册")
	}
	m := p.Manifest()
	if !m.Capabilities.Chat || !m.Capabilities.LocalImport {
		t.Fatalf("capabilities = %+v", m.Capabilities)
	}
	if len(m.Models) < 10 {
		t.Fatalf("models = %d", len(m.Models))
	}
	found := false
	for _, model := range m.Models {
		if model.ID == "kiro/claude-sonnet-4-6" && model.UpstreamModel == "claude-sonnet-4.6" {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺少 claude-sonnet-4.6: %+v", m.Models)
	}
}

func TestBuildConversation(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		Instructions: "你是助手",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "你好"},
			{Type: ir.ItemTypeMessage, Role: "assistant", Text: "你好呀"},
			{Type: ir.ItemTypeFunctionCall, CallID: "call-1", Name: "read_file", Arguments: `{"path":"main.go"}`},
			{Type: ir.ItemTypeFunctionCallOutput, CallID: "call-1", Output: "package main"},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "继续"},
		},
		Tools: []ir.UnifiedToolSpec{{Name: "read_file", Description: "读文件", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}},
	}
	current, history := buildConversation(req, "claude-sonnet-4.6")
	if len(history) < 2 {
		t.Fatalf("history = %+v", history)
	}
	userMsg, ok := current["userInputMessage"].(map[string]any)
	if !ok {
		t.Fatalf("current = %+v", current)
	}
	ctxMap, _ := userMsg["userInputMessageContext"].(map[string]any)
	if ctxMap == nil || ctxMap["tools"] == nil {
		t.Fatalf("tools 未挂载: %+v", userMsg)
	}
	first, _ := history[0].(map[string]any)
	firstUser, _ := first["userInputMessage"].(map[string]any)
	if firstUser == nil || !strings.Contains(firstUser["content"].(string), "System: 你是助手") {
		t.Fatalf("system 未注入首条历史: %+v", first)
	}
}

func TestChatStreamAWSEventStream(t *testing.T) {
	var gotPath, gotAuth, gotTokenType, gotAccept string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotTokenType = r.Header.Get("tokentype")
		gotAccept = r.Header.Get("Accept")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		frames := []struct {
			key     string
			payload string
		}{
			{"assistantResponseEvent", `{"content":"你"}`},
			{"reasoningContentEvent", `{"text":"想"}`},
			{"toolUseEvent", `{"name":"read_file","toolUseId":"call_1","input":{"path":"main.go"}}`},
			{"metadataEvent", `{"tokenUsage":{"uncachedInputTokens":3,"outputTokens":4,"totalTokens":7,"cacheReadInputTokens":1},"stopReason":"TOOL_USE"}`},
		}
		for _, f := range frames {
			_, _ = w.Write(wirecodec.EncodeAWSMessage(map[string]string{":event-type": f.key}, []byte(f.payload)))
		}
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc-1", "kiro")
	env.Credentials = map[string]string{
		"access_token": "ksk_test",
		"profile_arn":  "arn:aws:codewhisperer:us-east-1:123:profile/test",
		"region":       "eu-central-1",
		"base_url":     srv.URL,
	}
	env.HTTPClient = srv.Client()

	p := &Provider{}
	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "kiro/claude-sonnet-4-6", UpstreamModel: "claude-sonnet-4.6", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text, reasoning, toolName, toolArgs, finish string
	var usage *ir.Usage
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
	if text != "你" || reasoning != "想" {
		t.Fatalf("text=%q reasoning=%q", text, reasoning)
	}
	if toolName != "read_file" || toolArgs != `{"path":"main.go"}` {
		t.Fatalf("tool=%q args=%q", toolName, toolArgs)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish=%q", finish)
	}
	if usage == nil || usage.InputTokens != 3 || usage.OutputTokens != 4 || usage.CachedTokens != 1 {
		t.Fatalf("usage=%+v", usage)
	}
	if gotPath != "/generateAssistantResponse" {
		t.Fatalf("path=%q", gotPath)
	}
	if gotAuth != "Bearer ksk_test" || gotTokenType != "API_KEY" {
		t.Fatalf("auth=%q tokentype=%q", gotAuth, gotTokenType)
	}
	if gotAccept != "application/vnd.amazon.eventstream" {
		t.Fatalf("accept=%q", gotAccept)
	}
	state, _ := gotBody["conversationState"].(map[string]any)
	if state == nil || gotBody["profileArn"] == nil {
		t.Fatalf("body=%+v", gotBody)
	}
	if state["chatTriggerType"] != "MANUAL" {
		t.Fatalf("state=%+v", state)
	}
}

func TestChatStreamThrottling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		_, _ = w.Write(wirecodec.EncodeAWSMessage(
			map[string]string{":message-type": "exception", ":exception-type": "ThrottlingException"},
			[]byte(`{"message":"slow down","reason":"RATE"}`)))
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc", "kiro")
	env.Credentials = map[string]string{
		"access_token": "tk", "profile_arn": "arn:aws:codewhisperer:us-east-1:1:profile/x", "base_url": srv.URL,
	}
	env.HTTPClient = srv.Client()
	ch, err := (&Provider{}).ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "kiro/auto", UpstreamModel: "auto", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		if ae := ir.AsAPIError(err); ae == nil || ae.Code != ir.ErrCodeUpstreamRateLimit {
			t.Fatalf("err=%v", err)
		}
		return
	}
	var got error
	for ev := range ch {
		if ev.Type == ir.EventError {
			got = ev.Err
		}
	}
	if got == nil {
		t.Fatal("应返回流内限流错误")
	}
	if ae := ir.AsAPIError(got); ae == nil || ae.Code != ir.ErrCodeUpstreamRateLimit {
		t.Fatalf("err=%v", got)
	}
}

func TestLocalImportMissing(t *testing.T) {
	envHome := t.TempDir()
	t.Setenv("USERPROFILE", envHome)
	t.Setenv("HOME", envHome)
	if _, err := (&Provider{}).DiscoverLocalAccounts(context.Background()); err == nil {
		t.Fatal("没有账号文件时应报错")
	}
}

func TestRegionFromProfileArn(t *testing.T) {
	if got := regionFromProfileArn("arn:aws:codewhisperer:eu-central-1:123:profile/x"); got != "eu-central-1" {
		t.Fatalf("region = %q", got)
	}
	if got := regionFromProfileArn("bad-arn"); got != "" {
		t.Fatalf("region = %q", got)
	}
}

func TestHTTPErrorMapping(t *testing.T) {
	for status, want := range map[int]string{
		401: ir.ErrCodeUpstreamAuth,
		429: ir.ErrCodeUpstreamRateLimit,
		400: ir.ErrCodeBadRequest,
		502: ir.ErrCodeUpstreamFailed,
	} {
		if ae := mapHTTPError(status, "x", nil); ae == nil || ae.Code != want {
			t.Errorf("status %d => %+v", status, ae)
		}
	}
}

func TestPaymentRequiredMapsToFundsError(t *testing.T) {
	ae := mapHTTPError(402, `{"message":"Insufficient account funds"}`, nil)
	if ae == nil || !ae.Retryable || ae.StatusCode() != 402 {
		t.Fatalf("402 映射 = %+v", ae)
	}
	if !contains(ae.Message, "余额不足") {
		t.Fatalf("message = %q", ae.Message)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && strings.Contains(s, sub))
}
