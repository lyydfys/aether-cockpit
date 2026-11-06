package workbuddy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestManifest(t *testing.T) {
	cases := []struct {
		id         string
		models     int
		firstModel string
	}{
		{"workbuddy-cn", 15, "deepseek-v4.1-flash"},
		{"workbuddy-intl", 17, "deepseek-v4.1-flash"},
	}

	for _, tc := range cases {
		p, ok := provider.Get(tc.id)
		if !ok {
			t.Fatalf("%s 未注册", tc.id)
		}
		m := p.Manifest()

		if m.ID != tc.id || m.Family != "workbuddy" {
			t.Fatalf("%s manifest = %+v", tc.id, m)
		}
		if !m.Capabilities.Chat {
			t.Fatalf("%s Chat 能力 = %+v", tc.id, m.Capabilities)
		}
		if m.Capabilities.LocalImport != localImportSupported {
			t.Fatalf("%s LocalImport = %v, want %v", tc.id, m.Capabilities.LocalImport, localImportSupported)
		}
		if len(m.Models) != tc.models {
			t.Fatalf("%s 模型数 = %d, want %d", tc.id, len(m.Models), tc.models)
		}

		found := false
		for _, model := range m.Models {
			if model.ID == tc.id+"/"+tc.firstModel && model.UpstreamModel == tc.firstModel {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s 缺少模型 %s: %+v", tc.id, tc.firstModel, m.Models)
		}
	}

	if p, _ := provider.Get("workbuddy-intl"); p.Manifest().NetworkPolicy != provider.NetPolicyProxyFirst {
		t.Fatalf("Intl 网络策略 = %s", p.Manifest().NetworkPolicy)
	}
	if p, _ := provider.Get("workbuddy-cn"); p.Manifest().NetworkPolicy != provider.NetPolicyDirectForce {
		t.Fatalf("CN 网络策略 = %s", p.Manifest().NetworkPolicy)
	}
}

func TestChatStream(t *testing.T) {
	cases := []struct {
		prof      profile
		domain    string
		agentType string
	}{
		{cnProfile, "www.workbuddy.cn", ""},
		{intlProfile, "www.workbuddy.ai", "main"},
	}

	for _, tc := range cases {
		t.Run(tc.prof.id, func(t *testing.T) {
			var (
				gotPath, gotAuth, gotUID, gotDomain, gotUA string
				gotEnterprise, gotTenant, gotAgentType     string
				gotConvID, gotConvReq, gotConvMsg, gotReq  string
				gotBody                                    map[string]any
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				gotUID = r.Header.Get("X-User-Id")
				gotDomain = r.Header.Get("X-Domain")
				gotUA = r.Header.Get("User-Agent")
				gotEnterprise = r.Header.Get("X-Enterprise-Id")
				gotTenant = r.Header.Get("X-Tenant-Id")
				gotAgentType = r.Header.Get("X-Agent-Type")
				gotConvID = r.Header.Get("X-Conversation-ID")
				gotConvReq = r.Header.Get("X-Conversation-Request-ID")
				gotConvMsg = r.Header.Get("X-Conversation-Message-ID")
				gotReq = r.Header.Get("X-Request-ID")

				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &gotBody)

				w.Header().Set("Content-Type", "text/event-stream")
				frames := []string{
					`{"choices":[{"delta":{"reasoning_content":"想"}}]}`,
					`{"choices":[{"delta":{"content":"你"}}]}`,
					`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","function":{"name":"get_weather","arguments":"{\"city\":"}}]}}]}`,
					`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"SZ\"}"}}]}}]}`,
					`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
					`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7,"completion_tokens_details":{"reasoning_tokens":2}}}`,
				}
				for _, frame := range frames {
					_, _ = fmt.Fprintf(w, "data: %s\n\n", frame)
				}
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer srv.Close()

			p := newProvider(tc.prof)
			env := provider.NewAccountEnv("acc-1", tc.prof.id)
			env.Credentials = map[string]string{
				"token":    "tk",
				"user_id":  "uid-1",
				"base_url": srv.URL,
			}
			env.HTTPClient = srv.Client()

			temp := 0.3
			maxTokens := 128
			req := &ir.UnifiedChatRequest{
				Model:           tc.prof.id + "/hy3",
				UpstreamModel:   "hy3",
				Stream:          true,
				Instructions:    "你是助手",
				Temperature:     &temp,
				MaxOutputTokens: &maxTokens,
				ReasoningEffort: "high",
				Items: []ir.UnifiedItem{
					{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
					{Type: ir.ItemTypeReasoning, Text: "先想"},
					{Type: ir.ItemTypeFunctionCall, CallID: "call-1", Name: "get_weather", Arguments: `{"city":"SZ"}`},
					{Type: ir.ItemTypeFunctionCallOutput, CallID: "call-1", Output: "晴"},
				},
				Tools: []ir.UnifiedToolSpec{{
					Name:        "get_weather",
					Description: "查询天气",
					Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
				}},
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

			if text != "你" || reasoning != "想" {
				t.Fatalf("text=%q reasoning=%q", text, reasoning)
			}
			if toolName != "get_weather" || toolArgs != `{"city":"SZ"}` {
				t.Fatalf("tool=%q args=%q", toolName, toolArgs)
			}
			if finish != "tool_calls" {
				t.Fatalf("finish=%q", finish)
			}
			if usage == nil || usage.InputTokens != 3 || usage.OutputTokens != 4 ||
				usage.TotalTokens != 7 || usage.ReasoningTokens != 2 {
				t.Fatalf("usage=%+v", usage)
			}

			if gotPath != "/chat/completions" {
				t.Fatalf("path=%q", gotPath)
			}
			if gotAuth != "Bearer tk" || gotUID != "uid-1" {
				t.Fatalf("auth=%q uid=%q", gotAuth, gotUID)
			}
			if gotDomain != tc.domain {
				t.Fatalf("domain=%q want=%q", gotDomain, tc.domain)
			}
			if gotUA != userAgent {
				t.Fatalf("user-agent=%q", gotUA)
			}
			if gotEnterprise != "" || gotTenant != "" {
				t.Fatalf("enterprise=%q tenant=%q", gotEnterprise, gotTenant)
			}
			if gotAgentType != tc.agentType {
				t.Fatalf("agent-type=%q want=%q", gotAgentType, tc.agentType)
			}
			for name, value := range map[string]string{
				"X-Conversation-ID":         gotConvID,
				"X-Conversation-Request-ID": gotConvReq,
				"X-Conversation-Message-ID": gotConvMsg,
				"X-Request-ID":              gotReq,
			} {
				if value == "" {
					t.Fatalf("%s 为空", name)
				}
			}

			if gotBody["model"] != "hy3" || gotBody["stream"] != true {
				t.Fatalf("body=%+v", gotBody)
			}
			if gotBody["reasoning_effort"] != "high" {
				t.Fatalf("reasoning_effort=%v", gotBody["reasoning_effort"])
			}
			if gotBody["max_tokens"] != float64(128) || gotBody["temperature"] != 0.3 {
				t.Fatalf("采样参数 = %+v", gotBody)
			}

			messages, _ := gotBody["messages"].([]any)
			if len(messages) != 4 {
				t.Fatalf("messages=%+v", messages)
			}
			system, _ := messages[0].(map[string]any)
			user, _ := messages[1].(map[string]any)
			assistant, _ := messages[2].(map[string]any)
			tool, _ := messages[3].(map[string]any)

			if system["role"] != "system" || system["content"] != "你是助手" {
				t.Fatalf("system=%+v", system)
			}
			if user["role"] != "user" || user["content"] != "hi" {
				t.Fatalf("user=%+v", user)
			}
			if assistant["role"] != "assistant" || assistant["content"] != nil {
				t.Fatalf("assistant=%+v", assistant)
			}
			if assistant["reasoning_content"] != "先想" {
				t.Fatalf("reasoning_content=%v", assistant["reasoning_content"])
			}
			calls, _ := assistant["tool_calls"].([]any)
			if len(calls) != 1 {
				t.Fatalf("tool_calls=%+v", assistant["tool_calls"])
			}
			call, _ := calls[0].(map[string]any)
			fn, _ := call["function"].(map[string]any)
			if call["id"] != "call-1" || fn["name"] != "get_weather" || fn["arguments"] != `{"city":"SZ"}` {
				t.Fatalf("call=%+v", call)
			}
			if tool["role"] != "tool" || tool["tool_call_id"] != "call-1" || tool["content"] != "晴" {
				t.Fatalf("tool=%+v", tool)
			}

			tools, _ := gotBody["tools"].([]any)
			if len(tools) != 1 {
				t.Fatalf("tools=%+v", gotBody["tools"])
			}
		})
	}
}

func TestConversationIDsStable(t *testing.T) {
	req := &ir.UnifiedChatRequest{
		SessionKey: "session-1",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "第一句"},
			{Type: ir.ItemTypeMessage, Role: "assistant", Text: "回答"},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "第二句"},
		},
	}

	conv1, reqID1, msgID1 := conversationIDs(req, false)
	conv2, reqID2, msgID2 := conversationIDs(req, false)
	if conv1 != conv2 || reqID1 != reqID2 || msgID1 != msgID2 {
		t.Fatalf("同一逻辑发送的标识不稳定: %s/%s/%s vs %s/%s/%s",
			conv1, reqID1, msgID1, conv2, reqID2, msgID2)
	}

	next := req.Clone()
	next.Items[len(next.Items)-1].Text = "第三句"
	_, reqID3, msgID3 := conversationIDs(next, false)
	if reqID1 == reqID3 {
		t.Fatal("不同用户文本应有不同的 Conversation-Request-ID")
	}
	if msgID1 == msgID3 {
		t.Fatal("不同消息轮次应有不同的 Conversation-Message-ID")
	}
}

func TestMapUpstreamError(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		code      string
		retryable bool
		contains  string
	}{
		{"401", 401, `{"message":"unauthorized"}`, ir.ErrCodeUpstreamAuth, false, "unauthorized"},
		{"403", 403, `{"message":"forbidden"}`, ir.ErrCodeUpstreamAuth, false, "forbidden"},
		{"429", 429, `{"message":"too many"}`, ir.ErrCodeUpstreamRateLimit, true, "too many"},
		{"10085", 400, `{"code":10085,"message":"bad user-agent"}`, ir.ErrCodeBadRequest, false, "bad user-agent"},
		{"11128", 400, `{"code":11128,"message":"bad model"}`, ir.ErrCodeBadRequest, false, "bad model"},
		{"11133", 400, `{"code":11133,"message":"bad payload"}`, ir.ErrCodeBadRequest, false, "bad payload"},
		{"11153", 400, `{"code":11153,"message":"bad tool"}`, ir.ErrCodeBadRequest, false, "bad tool"},
		{"11140", 400, `{"code":11140,"message":"bad turn"}`, ir.ErrCodeUpstreamRateLimit, true, "bad turn"},
		{"11142", 400, `{"code":11142,"message":"bad turn"}`, ir.ErrCodeUpstreamRateLimit, true, "bad turn"},
		{"14015", 401, `{"code":14015,"message":"expired"}`, ir.ErrCodeUpstreamAuth, false, "expired"},
		{"6005", 200, `{"code":6005,"message":"quota"}`, ir.ErrCodeUpstreamRateLimit, true, "quota"},
		{"500", 500, `{"error":{"message":"boom"}}`, ir.ErrCodeUpstreamFailed, true, "boom"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ae := mapUpstreamError(tc.status, tc.body, nil)
			if ae.Code != tc.code {
				t.Fatalf("code=%q want=%q", ae.Code, tc.code)
			}
			if ae.Retryable != tc.retryable {
				t.Fatalf("retryable=%v want=%v", ae.Retryable, tc.retryable)
			}
			if !strings.Contains(ae.Message, tc.contains) {
				t.Fatalf("message=%q 缺少 %q", ae.Message, tc.contains)
			}
		})
	}
}

func TestChatStreamMapsUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":10085,"message":"User-Agent must start with WorkBuddy"}`)
	}))
	defer srv.Close()

	p := newProvider(cnProfile)
	env := provider.NewAccountEnv("acc", "workbuddy-cn")
	env.Credentials = map[string]string{"token": "tk", "user_id": "uid", "base_url": srv.URL}
	env.HTTPClient = srv.Client()

	_, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "workbuddy-cn/hy3", UpstreamModel: "hy3", Stream: true,
	})
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeBadRequest {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(ae.Message, "User-Agent must start with WorkBuddy") {
		t.Fatalf("message=%q", ae.Message)
	}
}

func TestParseLocalAuthPlaintext(t *testing.T) {
	raw := []byte(`{
	  "auth": {"accessToken": "tk-local", "tokenType": "Bearer", "domain": "custom.workbuddy.cn"},
	  "account": {"uid": "uid-local", "nickname": "本地账号"}
	}`)

	draft, err := parseLocalAuth(raw, cnProfile, "/tmp/workbuddy-desktop.info")
	if err != nil {
		t.Fatal(err)
	}
	if draft == nil {
		t.Fatal("应解析出草稿")
	}
	if draft.Label != "本地账号" {
		t.Fatalf("label=%q", draft.Label)
	}
	if draft.Credentials["token"] != "tk-local" || draft.Credentials["user_id"] != "uid-local" ||
		draft.Credentials["domain"] != "custom.workbuddy.cn" {
		t.Fatalf("credentials=%+v", draft.Credentials)
	}
	if !strings.HasPrefix(draft.Source, "local_import:") {
		t.Fatalf("source=%q", draft.Source)
	}
}

func TestParseLocalAuthNicknameFallback(t *testing.T) {
	raw := []byte(`{
	  "auth": {"accessToken": "tk", "domain": ""},
	  "account": {"uid": "uid-2", "nickname": {"ciphertext": "sealed"}}
	}`)

	draft, err := parseLocalAuth(raw, intlProfile, "/tmp/workbuddy-desktop-ai.info")
	if err != nil {
		t.Fatal(err)
	}
	if draft == nil || draft.Label != "uid-2" {
		t.Fatalf("draft=%+v", draft)
	}
	if draft.Credentials["domain"] != intlProfile.domain {
		t.Fatalf("domain=%q", draft.Credentials["domain"])
	}
}

func TestParseLocalAuthSealedEnvelope(t *testing.T) {
	if !localImportSupported {
		t.Skip("本机登录态解密仅 Windows")
	}
	raw := []byte(`{
	  "auth": {"accessToken": {"suite": "aes-256-gcm", "ciphertext": "c2VhbGVk"}},
	  "account": {"uid": "uid-1", "nickname": "账号"}
	}`)

	draft, err := parseLocalAuth(raw, cnProfile, "/tmp/workbuddy-desktop.info")
	if draft != nil {
		t.Fatalf("加密信封不应产出草稿: %+v", draft)
	}
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeNoAccount {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(ae.Message, "解密失败") {
		t.Fatalf("message=%q", ae.Message)
	}
	if strings.Contains(ae.Message, "c2VhbGVk") {
		t.Fatal("错误信息不应携带密文")
	}
}

func TestDiscoverLocalAccountsFromLocalAppData(t *testing.T) {
	if !localImportSupported {
		t.Skip("本机登录态导入仅 Windows")
	}
	dir := t.TempDir()
	authDir := filepath.Join(dir, "CodeBuddyExtension", "Data", "Public", "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	infoPath := filepath.Join(authDir, "workbuddy-desktop.info")
	if err := os.WriteFile(infoPath, []byte(`{
	  "auth": {"accessToken": "tk-file", "tokenType": "Bearer", "domain": ""},
	  "account": {"uid": "uid-file", "nickname": "文件账号"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LOCALAPPDATA", dir)

	p := newProvider(cnProfile)
	drafts, err := p.DiscoverLocalAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 {
		t.Fatalf("drafts=%d", len(drafts))
	}
	if drafts[0].Label != "文件账号" ||
		drafts[0].Credentials["token"] != "tk-file" ||
		drafts[0].Credentials["user_id"] != "uid-file" ||
		drafts[0].Credentials["domain"] != cnProfile.domain {
		t.Fatalf("draft=%+v", drafts[0])
	}
}

func TestDiscoverLocalAccountsMissing(t *testing.T) {
	if !localImportSupported {
		t.Skip("本机登录态导入仅 Windows")
	}
	t.Setenv("LOCALAPPDATA", t.TempDir())

	p := newProvider(intlProfile)
	_, err := p.DiscoverLocalAccounts(context.Background())
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeNoAccount {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(ae.Message, "手动") {
		t.Fatalf("message=%q", ae.Message)
	}
}

// 11128 是"通道校验拦截"，上游按 id 记住被拦的那一次。fresh=true 必须给出
// 全新的一组 id，否则重放还是同一条，必然再被拦。
func TestConversationIDsFresh(t *testing.T) {
	req := &ir.UnifiedChatRequest{Items: []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "同一个问题"},
	}}
	a1, r1, m1 := conversationIDs(req, true)
	a2, r2, m2 := conversationIDs(req, true)
	if a1 == a2 || r1 == r2 || m1 == m2 {
		t.Fatalf("fresh 应每次不同: %s/%s/%s vs %s/%s/%s", a1, r1, m1, a2, r2, m2)
	}
	stable, _, _ := conversationIDs(req, false)
	if stable == a1 {
		t.Fatal("fresh 不应退回会话哈希值")
	}
}

func TestChannelBlocked(t *testing.T) {
	yes := `{"code":11128,"msg":"Illegal API invocation from an unapproved channel"}`
	yes2 := `{"code": 11128, "msg":"..."}`
	no := `{"code":11127,"msg":"other"}`
	if !channelBlocked(yes) || !channelBlocked(yes2) {
		t.Fatal("应识别 11128")
	}
	if channelBlocked(no) {
		t.Fatal("不该把 11127 当成通道拦截")
	}
}

// 11128 拦下后必须换一组会话 id 重放，且第二次成功就不要再报错。
func TestChatStreamRetriesChannelBlockWithFreshIDs(t *testing.T) {
	var convIDs []string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		convIDs = append(convIDs, r.Header.Get("X-Conversation-ID"))
		if calls == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"code":11128,"msg":"Illegal API invocation from an unapproved channel"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	p := newProvider(cnProfile)
	env := provider.NewAccountEnv("acc", "workbuddy-cn")
	env.Credentials = map[string]string{"token": "tk", "user_id": "uid", "base_url": srv.URL}
	env.HTTPClient = srv.Client()

	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "workbuddy-cn/hy3", UpstreamModel: "hy3", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("应重放成功，实际报错: %v", err)
	}
	for range ch {
	}
	if calls != 2 {
		t.Fatalf("应请求两次，实际 %d 次", calls)
	}
	if len(convIDs) == 2 && convIDs[0] == convIDs[1] {
		t.Fatalf("重放必须换会话 id，两次都是 %s", convIDs[0])
	}
}
