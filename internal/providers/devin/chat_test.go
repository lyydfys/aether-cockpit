package devin

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

// devinTestUpstream 模拟 Connect-RPC 上游：可断言请求头/体，回放给定帧。
func devinTestUpstream(t *testing.T, status int, frames [][]byte, gotReq **http.Request, gotBody *[]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != devinChatPath {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		if gotReq != nil {
			*gotReq = r.Clone(context.Background())
		}
		if gotBody != nil {
			*gotBody = raw
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"mock devin error"}`)
			return
		}
		w.Header().Set("Content-Type", "application/connect+proto")
		w.WriteHeader(http.StatusOK)
		for _, f := range frames {
			_, _ = w.Write(f)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func devinTextFrame(text, thinking string) []byte {
	var payload []byte
	if text != "" {
		payload = appendTestBytes(payload, 3, []byte(text))
	}
	if thinking != "" {
		payload = appendTestBytes(payload, 9, []byte(thinking))
	}
	return WrapConnectEnvelope(payload)
}

func devinToolFrame(id, name, args string, stop uint64) []byte {
	var tc []byte
	tc = appendTestBytes(tc, 1, []byte(id))
	tc = appendTestBytes(tc, 2, []byte(name))
	tc = appendTestBytes(tc, 3, []byte(args))
	var payload []byte
	payload = appendTestBytes(payload, 6, tc)
	if stop != 0 {
		payload = appendTestVarint(payload, 5, stop)
	}
	return WrapConnectEnvelope(payload)
}

func devinUsageFrame(prompt, completion, cached uint64) []byte {
	var usage []byte
	usage = appendTestVarint(usage, 2, prompt)
	usage = appendTestVarint(usage, 3, completion)
	usage = appendTestVarint(usage, 5, cached)
	var payload []byte
	payload = appendTestBytes(payload, 7, usage)
	return WrapConnectEnvelope(payload)
}

func devinTrailerFrame(body string) []byte {
	return wrapConnectEnvelopeWithFlag(ConnectFlagEndStream, []byte(body))
}

func devinEnv(t *testing.T, srv *httptest.Server, creds map[string]string) *provider.AccountEnv {
	t.Helper()
	env := provider.NewAccountEnv("acc-devin", channelID)
	env.Credentials = map[string]string{"base_url": srv.URL}
	for k, v := range creds {
		env.Credentials[k] = v
	}
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	return env
}

func collectDevin(t *testing.T, ch <-chan ir.UnifiedStreamEvent) (ir.AggregatedResponse, []ir.UnifiedStreamEvent) {
	t.Helper()
	acc := ir.NewAccumulator()
	var events []ir.UnifiedStreamEvent
	for ev := range ch {
		events = append(events, ev)
		if ev.Type == ir.EventError {
			t.Fatalf("流内错误: %v", ev.Err)
		}
		acc.Push(ev)
	}
	return acc.Result(), events
}

func TestDevinChatStream(t *testing.T) {
	frames := [][]byte{
		devinTextFrame("你好", "先想一想"),
		devinTextFrame("，世界", ""),
		devinToolFrame("call-1", "get_weather", `{"city":"SH"}`, 10),
		devinUsageFrame(11, 7, 3),
		devinTrailerFrame(`{}`),
	}
	var gotReq *http.Request
	var gotBody []byte
	srv := devinTestUpstream(t, http.StatusOK, frames, &gotReq, &gotBody)

	p := &Provider{}
	env := devinEnv(t, srv, map[string]string{"session_token": "eyJabc"})
	req := &ir.UnifiedChatRequest{
		Model:        "devin/swe-2",
		Instructions: "you are devin",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "天气如何"},
		},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	res, events := collectDevin(t, ch)

	if res.Text != "你好，世界" {
		t.Fatalf("文本聚合不符: %q", res.Text)
	}
	if res.ReasoningText != "先想一想" {
		t.Fatalf("思考聚合不符: %q", res.ReasoningText)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "get_weather" ||
		!strings.Contains(res.ToolCalls[0].Arguments, "SH") {
		t.Fatalf("工具调用不符: %+v", res.ToolCalls)
	}
	if res.Usage.InputTokens != 11 || res.Usage.OutputTokens != 7 || res.Usage.CachedTokens != 3 {
		t.Fatalf("用量不符: %+v", res.Usage)
	}
	if res.FinishReason != "tool_calls" {
		t.Fatalf("finish=%q", res.FinishReason)
	}

	var sawUsage, sawDone bool
	for _, ev := range events {
		if ev.Type == ir.EventUsage {
			sawUsage = true
		}
		if ev.Type == ir.EventDone {
			sawDone = true
		}
	}
	if !sawUsage || !sawDone {
		t.Fatalf("缺少 usage/done 事件: %d 个事件", len(events))
	}

	// 请求侧：Basic token-token、connect 协议头、UA 抑制、会话 token 与模型 UID 上线。
	if got := gotReq.Header.Get("Authorization"); got != "Basic devin-session-token$eyJabc-devin-session-token$eyJabc" {
		t.Fatalf("Authorization=%q", got)
	}
	if got := gotReq.Header.Get("Content-Type"); got != "application/connect+proto" {
		t.Fatalf("Content-Type=%q", got)
	}
	if got := gotReq.Header.Get("Connect-Protocol-Version"); got != "1" {
		t.Fatalf("Connect-Protocol-Version=%q", got)
	}
	// 原生 CLI 不发 UA：Go 侧置空串后，线上要么没有该头，要么是空值。
	if ua, ok := gotReq.Header["User-Agent"]; ok && !(len(ua) == 1 && ua[0] == "") {
		t.Fatalf("User-Agent 未抑制: %v", ua)
	}
	if gotReq.Header.Get("Sentry-Trace") == "" {
		t.Fatal("缺少 Sentry-Trace")
	}
	flag, payload, err := ReadConnectFrame(strings.NewReader(string(gotBody)))
	if err != nil || flag != ConnectFlagData {
		t.Fatalf("请求不是合法 Connect 帧: flag=%d err=%v", flag, err)
	}
	for _, want := range []string{"you are devin", "天气如何", "swe-2-high"} {
		if !strings.Contains(string(payload), want) {
			t.Fatalf("请求体缺少 %q", want)
		}
	}
}

func TestDevinChatHTTPAuthError(t *testing.T) {
	srv := devinTestUpstream(t, http.StatusUnauthorized, nil, nil, nil)
	p := &Provider{}
	env := devinEnv(t, srv, map[string]string{"session_token": "tok"})
	_, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "devin/swe-2",
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err == nil {
		t.Fatal("期望鉴权错误")
	}
	ae, ok := err.(*ir.APIError)
	if !ok || ae.Code != ir.ErrCodeUpstreamAuth {
		t.Fatalf("错误不符: %v", err)
	}
}

func TestDevinChatTrailerError(t *testing.T) {
	frames := [][]byte{
		devinTextFrame("partial", ""),
		devinTrailerFrame(`{"error":{"code":"failed_precondition","message":"User monthly ACU quota exhausted"}}`),
	}
	srv := devinTestUpstream(t, http.StatusOK, frames, nil, nil)
	p := &Provider{}
	env := devinEnv(t, srv, map[string]string{"session_token": "tok"})
	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "devin/swe-2",
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	var lastErr *ir.APIError
	for ev := range ch {
		if ev.Type == ir.EventError && ev.Err != nil {
			lastErr, _ = ev.Err.(*ir.APIError)
		}
	}
	if lastErr == nil || lastErr.Code != ir.ErrCodeUpstreamRateLimit {
		t.Fatalf("trailer 错误未映射为限流: %+v", lastErr)
	}
}

func TestDevinChatRequiresUserTurn(t *testing.T) {
	p := &Provider{}
	env := provider.NewAccountEnv("acc", channelID)
	env.Credentials = map[string]string{"session_token": "tok"}
	_, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "devin/swe-2",
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "assistant", Text: "hi"}},
	})
	if err == nil {
		t.Fatal("期望缺少用户消息的错误")
	}
}

func TestDevinChatMissingToken(t *testing.T) {
	p := &Provider{}
	env := provider.NewAccountEnv("acc", channelID)
	_, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{Model: "devin/swe-2"})
	if err == nil {
		t.Fatal("期望缺少 token 的错误")
	}
	ae, ok := err.(*ir.APIError)
	if !ok || ae.Code != ir.ErrCodeUpstreamAuth {
		t.Fatalf("错误不符: %v", err)
	}
}
