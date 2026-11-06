package codebuddy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const cbSSEFixture = `data: {"id":"1","choices":[{"index":0,"delta":{"role":"assistant","content":"你好"}}]}

data: {"choices":[{"delta":{"content":"，世界"},"finish_reason":null}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7}}

data: [DONE]

`

// captureUpstream 记录最后一个请求的头与请求体，并回放固定 SSE。
func captureUpstream(t *testing.T, status int, respBody string) (*httptest.Server, *http.Request, *[]byte) {
	t.Helper()
	var gotReq http.Request
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotReq = *r.Clone(context.Background())
		gotBody = raw
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(respBody))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, &gotReq, &gotBody
}

func cbEnv(t *testing.T, srv *httptest.Server, providerID string, creds map[string]string) *provider.AccountEnv {
	t.Helper()
	env := provider.NewAccountEnv("acc-cb", providerID)
	env.Credentials = map[string]string{"base_url": srv.URL, "account_uid": "u-42"}
	for k, v := range creds {
		env.Credentials[k] = v
	}
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	return env
}

func collectCB(t *testing.T, ch <-chan ir.UnifiedStreamEvent) ir.AggregatedResponse {
	t.Helper()
	acc := ir.NewAccumulator()
	for ev := range ch {
		if ev.Type == ir.EventError {
			t.Fatalf("上游错误: %v", ev.Err)
		}
		acc.Push(ev)
	}
	return acc.Result()
}

func TestCodeBuddyChatStreamCN(t *testing.T) {
	srv, gotReq, gotBody := captureUpstream(t, http.StatusOK, cbSSEFixture)
	p := newProvider(cnProfile)
	env := cbEnv(t, srv, p.profile.id, map[string]string{"token": "tok-abc"})

	req := &ir.UnifiedChatRequest{
		Model:        "codebuddy-cn/glm-5.2",
		Instructions: "你是助手",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "developer", Text: "规则"},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "打个招呼"},
		},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	res := collectCB(t, ch)

	if !strings.Contains(res.Text, "你好") || !strings.Contains(res.Text, "，世界") {
		t.Fatalf("文本未聚合: %q", res.Text)
	}
	if res.Usage.InputTokens != 11 || res.Usage.OutputTokens != 7 {
		t.Fatalf("用量未透传: %+v", res.Usage)
	}
	if res.FinishReason != "stop" {
		t.Fatalf("finish_reason=%q", res.FinishReason)
	}

	// 头：鉴权、指纹、会话链路、账号
	if got := gotReq.Header.Get("Authorization"); got != "Bearer tok-abc" {
		t.Fatalf("Authorization=%q", got)
	}
	if got := gotReq.Header.Get("X-Domain"); got == "" {
		t.Fatal("缺少 X-Domain")
	}
	if got := gotReq.Header.Get("x-stainless-lang"); got != "js" {
		t.Fatalf("x-stainless-lang=%q", got)
	}
	// 官方 CLI 的身份头必须齐：缺了就和开源参考实现对不上，等于自报"非官方"。
	if got := gotReq.Header.Get("X-IDE-Name"); got != "CLI" {
		t.Fatalf("X-IDE-Name=%q", got)
	}
	if got := gotReq.Header.Get("X-Agent-Intent"); got != "craft" {
		t.Fatalf("X-Agent-Intent=%q", got)
	}
	if got := gotReq.Header.Get("X-Client-Platform"); got != "web" {
		t.Fatalf("X-Client-Platform=%q", got)
	}
	if got := gotReq.Header.Get("X-Conversation-ID"); len(got) != 36 {
		t.Fatalf("X-Conversation-ID=%q", got)
	}
	if got := gotReq.Header.Get("X-User-Id"); got != "u-42" {
		t.Fatalf("X-User-Id=%q", got)
	}
	if got := gotReq.Header.Get("User-Agent"); !strings.HasPrefix(got, "CLI/2.107.0 CodeBuddy/") {
		t.Fatalf("User-Agent=%q", got)
	}

	// 体：developer→system、思考缺省、include_usage、模型去前缀
	var body map[string]any
	if err := json.Unmarshal(*gotBody, &body); err != nil {
		t.Fatalf("请求体不是 JSON: %v", err)
	}
	if body["model"] != "glm-5.2" {
		t.Fatalf("model=%v", body["model"])
	}
	if body["stream"] != true || body["enable_thinking"] != true || body["reasoning_effort"] != "medium" {
		t.Fatalf("缺省字段缺失: %v", body)
	}
	so, _ := body["stream_options"].(map[string]any)
	if so == nil || so["include_usage"] != true {
		t.Fatalf("stream_options=%v", body["stream_options"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages=%v", msgs)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "你是助手" {
		t.Fatalf("首条 system 丢失: %v", first)
	}
	second, _ := msgs[1].(map[string]any)
	if second["role"] != "system" {
		t.Fatalf("developer 未归一: %v", second)
	}
}

func TestCodeBuddyChatIntlRequiresLeadingSystem(t *testing.T) {
	srv, gotReq, gotBody := captureUpstream(t, http.StatusOK, cbSSEFixture)
	p := newProvider(intlProfile)
	env := cbEnv(t, srv, p.profile.id, map[string]string{"token": "tok-intl"})

	req := &ir.UnifiedChatRequest{
		Model: "codebuddy-intl/glm-5.2",
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	collectCB(t, ch)

	var body map[string]any
	_ = json.Unmarshal(*gotBody, &body)
	msgs, _ := body["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != cbLeadingSystem {
		t.Fatalf("国际站首条 system 未注入: %v", first)
	}
	if got := gotReq.Header.Get("X-Model-ID"); got != "glm-5.2" {
		t.Fatalf("X-Model-ID=%q", got)
	}
	if got := gotReq.Header.Get("X-Product-Version"); got != cbIntlCLIVersion {
		t.Fatalf("X-Product-Version=%q", got)
	}
	if got := gotReq.Header.Get("User-Agent"); !strings.HasPrefix(got, "CLI/2.178.0") {
		t.Fatalf("User-Agent=%q", got)
	}
}

func TestCodeBuddyChatIntlSkipsLeadingSystemWhenPresent(t *testing.T) {
	srv, _, gotBody := captureUpstream(t, http.StatusOK, cbSSEFixture)
	p := newProvider(intlProfile)
	env := cbEnv(t, srv, p.profile.id, map[string]string{"token": "tok-intl"})

	req := &ir.UnifiedChatRequest{
		Model:        "codebuddy-intl/glm-5.2",
		Instructions: "自带系统词",
		Items:        []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, _ := p.ChatStream(context.Background(), env, req)
	collectCB(t, ch)

	var body map[string]any
	_ = json.Unmarshal(*gotBody, &body)
	msgs, _ := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("不应重复注入 system: %v", msgs)
	}
	first, _ := msgs[0].(map[string]any)
	if first["content"] != "自带系统词" {
		t.Fatalf("首条被覆盖: %v", first)
	}
}

func TestCodeBuddyErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"model blocked", 400, `{"code":11158,"displayMsg":"model not allowed"}`, ir.ErrCodeBadRequest},
		{"model rate limited", 429, `{"code":6004,"displayMsg":"model quota exceeded"}`, ir.ErrCodeUpstreamRateLimit},
		{"risk control", 400, `{"code":11128,"displayMsg":"Illegal API invocation"}`, ir.ErrCodeUpstreamFailed},
		{"account restricted", 403, `{"code":11140,"displayMsg":"account restricted"}`, ir.ErrCodeUpstreamAuth},
		{"credits exhausted", 400, `{"code":14018,"displayMsg":"credits exhausted"}`, ir.ErrCodeUpstreamFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _, _ := captureUpstream(t, tc.status, tc.body)
			p := newProvider(cnProfile)
			env := cbEnv(t, srv, p.profile.id, map[string]string{"token": "tok"})
			req := &ir.UnifiedChatRequest{Model: "codebuddy-cn/glm-5.2"}
			_, err := p.ChatStream(context.Background(), env, req)
			if err == nil {
				t.Fatal("期望错误")
			}
			ae, ok := err.(*ir.APIError)
			if !ok {
				t.Fatalf("错误类型 %T", err)
			}
			if ae.Code != tc.want {
				t.Fatalf("code=%q want %q (%v)", ae.Code, tc.want, err)
			}
		})
	}
}

func TestConversationUUIDStable(t *testing.T) {
	a := cbConversationUUID("seed-1")
	b := cbConversationUUID("seed-1")
	if a != b {
		t.Fatalf("同 seed 应稳定: %s vs %s", a, b)
	}
	if len(a) != 36 || strings.Count(a, "-") != 4 {
		t.Fatalf("不是 UUID 形态: %s", a)
	}
	if !strings.HasSuffix(a, "-426614174000") {
		t.Fatalf("node 段不符合官方 CLI: %s", a)
	}
	if c := cbConversationUUID("seed-2"); c == a {
		t.Fatal("不同 seed 不应相同")
	}
}

// 端点无视 stream=true 直接回整段 JSON 时，仍要产出完整文本与用量。
func TestCodeBuddyChatWholeJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"1","choices":[{"index":0,"message":{"role":"assistant","content":"json-ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
	}))
	defer srv.Close()

	p := newProvider(cnProfile)
	env := cbEnv(t, srv, p.profile.id, map[string]string{"token": "tok-json"})
	req := &ir.UnifiedChatRequest{
		Model: "codebuddy-cn/glm-5.2", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}
	res := collectCB(t, ch)
	if res.Text != "json-ok" || res.Usage.InputTokens != 3 || res.Usage.OutputTokens != 2 || res.FinishReason != "stop" {
		t.Fatalf("res = %+v", res)
	}
}
