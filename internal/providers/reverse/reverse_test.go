package reverse

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
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

func loadTestSpec(t *testing.T, raw string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	st := spec.NewStore()
	if err := st.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	spec.SetDefault(st)
	t.Cleanup(func() { spec.SetDefault(spec.NewStore()) })
}

func testProfile(id string) Profile {
	return Profile{
		ID:            id,
		DisplayName:   "Test Reverse",
		RegionTag:     "CN",
		NetworkPolicy: provider.NetPolicySystem,
		Auths:         []provider.AuthMethod{provider.AuthManualInput},
		Middlewares:   []string{"stateful_session"},
	}
}

func testEnv(id string, client *http.Client) *provider.AccountEnv {
	env := provider.NewAccountEnv("test-account", id)
	env.Credentials = map[string]string{"access_token": "tk", "refresh_token": "rt"}
	env.HTTPClient = client
	return env
}

func TestManifestMergesSpec(t *testing.T) {
	loadTestSpec(t, `{
	  "provider_id": "opencode",
	  "spec_version": "1",
	  "base_url": "https://example.com",
	  "models": {"sonnet": "claude-sonnet-4-5"},
	  "middlewares": ["stateful_session", "xml_tool_call"],
	  "oauth": {
	    "authorize_url": "https://example.com/auth",
	    "token_url": "https://example.com/token",
	    "device_code_url": "https://example.com/device",
	    "client_id": "cid"
	  }
	}`)

	p, ok := provider.Get("opencode")
	if !ok {
		t.Fatal("opencode 未注册")
	}
	m := p.Manifest()

	foundModel := false
	for _, model := range m.Models {
		if model.ID == "opencode/sonnet" && model.UpstreamModel == "claude-sonnet-4-5" {
			foundModel = true
		}
	}
	if !foundModel {
		t.Fatalf("模型清单未合并: %+v", m.Models)
	}
	if !m.HasMiddleware("xml_tool_call") {
		t.Fatal("middlewares 未按 spec 覆盖")
	}
	if !m.Capabilities.Refresh {
		t.Fatal("有 token_url 时应声明刷新能力")
	}
	if m.OAuth == nil || m.OAuth.ClientID != "cid" {
		t.Fatalf("OAuth 未合并: %+v", m.OAuth)
	}
	if !containsAuth(m.SupportedAuths, provider.AuthOAuthPKCE) || !containsAuth(m.SupportedAuths, provider.AuthDeviceCode) {
		t.Fatalf("OAuth 登录方式未追加: %v", m.SupportedAuths)
	}
}

func containsAuth(list []provider.AuthMethod, want provider.AuthMethod) bool {
	for _, a := range list {
		if a == want {
			return true
		}
	}
	return false
}

func TestChatStreamSpecMapping(t *testing.T) {
	var gotModel, gotToken string
	var gotMessages []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat" {
			t.Errorf("路径 = %s", r.URL.Path)
		}
		gotToken = r.Header.Get("X-Token")

		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel, _ = body["m"].(string)
		gotMessages, _ = body["msgs"].([]any)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"delta\":{\"say\":\"你好\",\"think\":\"想一下\"},\"stop\":\"stop\",\"use\":{\"in\":3,\"out\":4,\"all\":7}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"delta\":{\"say\":\"世界\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	loadTestSpec(t, fmt.Sprintf(`{
	  "provider_id": "test-reverse",
	  "spec_version": "1",
	  "base_url": %q,
	  "models": {"m1": "upstream-m"},
	  "endpoints": {"chat": "/chat"},
	  "extra": {
	    "chat": {
	      "auth": {"header": "X-Token", "no_prefix": true, "credential": "access_token"},
	      "request": {"model_field": "m", "messages_field": "msgs", "stream_field": "s"},
	      "response": {
	        "text_path": "delta.say",
	        "reasoning_paths": ["delta.think"],
	        "finish_path": "stop",
	        "usage_path": "use",
	        "usage_input_path": "in",
	        "usage_output_path": "out",
	        "usage_total_path": "all"
	      }
	    }
	  }
	}`, srv.URL))

	p := New(testProfile("test-reverse"))
	env := testEnv("test-reverse", srv.Client())

	req := &ir.UnifiedChatRequest{
		Model:         "test-reverse/m1",
		UpstreamModel: "m1",
		Stream:        true,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}

	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}

	var (
		text, reasoning, finish string
		usage                   *ir.Usage
	)
	for ev := range ch {
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

	if text != "你好世界" {
		t.Fatalf("文本 = %q", text)
	}
	if reasoning != "想一下" {
		t.Fatalf("思考 = %q", reasoning)
	}
	if finish != "stop" {
		t.Fatalf("finish = %q", finish)
	}
	if usage == nil || usage.InputTokens != 3 || usage.OutputTokens != 4 || usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v", usage)
	}
	if gotToken != "tk" {
		t.Fatalf("鉴权头 = %q", gotToken)
	}
	if gotModel != "upstream-m" {
		t.Fatalf("上游模型 = %q", gotModel)
	}
	if len(gotMessages) != 1 {
		t.Fatalf("messages = %+v", gotMessages)
	}
}

func TestChatStreamNotConfigured(t *testing.T) {
	// 不加载 spec：必须给出可执行的配置指引，而不是静默请求。
	spec.SetDefault(spec.NewStore())
	t.Cleanup(func() { spec.SetDefault(spec.NewStore()) })

	p := New(testProfile("not-configured"))
	env := testEnv("not-configured", http.DefaultClient)
	req := &ir.UnifiedChatRequest{Model: "not-configured/x", UpstreamModel: "x", Stream: true}

	_, err := p.ChatStream(context.Background(), env, req)
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeUnsupported {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(ae.Message, "configs/specs/not-configured.json") {
		t.Fatalf("错误信息缺少配置指引: %s", ae.Message)
	}
}

func TestLocalImportFromSpec(t *testing.T) {
	dir := t.TempDir()
	authFile := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authFile, []byte(`{
	  "auth": {"accessToken": "local-token", "refreshToken": "local-refresh", "expiresAt": "2030-01-02T03:04:05Z"},
	  "user": {"email": "user@example.com"}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}

	loadTestSpec(t, fmt.Sprintf(`{
	  "provider_id": "test-import",
	  "spec_version": "1",
	  "extra": {
	    "local_import": {
	      "paths": [%q],
	      "format": "json",
	      "label_path": "user.email",
	      "credentials": {"access_token": "auth.accessToken", "refresh_token": "auth.refreshToken"},
	      "expires_at_path": "auth.expiresAt"
	    }
	  }
	}`, strings.ReplaceAll(authFile, `\`, `\\`)))

	p := New(testProfile("test-import"))
	drafts, err := p.DiscoverLocalAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 {
		t.Fatalf("drafts = %d", len(drafts))
	}
	d := drafts[0]
	if d.Label != "user@example.com" {
		t.Fatalf("label = %q", d.Label)
	}
	if d.Credentials["access_token"] != "local-token" || d.Credentials["refresh_token"] != "local-refresh" {
		t.Fatalf("credentials = %+v", d.Credentials)
	}
	if d.ExpiresAt == 0 {
		t.Fatal("expires_at 未解析")
	}
	if !strings.HasPrefix(d.Source, "local_import:") {
		t.Fatalf("source = %q", d.Source)
	}
}

func TestResponsesPassthrough(t *testing.T) {
	var (
		gotModel string
		gotMeta  string
		gotAuth  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		gotModel, _ = body["model"].(string)
		gotMeta, _ = body["x_vendor_meta"].(string)
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {}\n\n")
	}))
	defer srv.Close()

	loadTestSpec(t, fmt.Sprintf(`{
	  "provider_id": "test-native",
	  "spec_version": "1",
	  "base_url": %q,
	  "endpoints": {"responses": "/responses"}
	}`, srv.URL))

	p := New(Profile{
		ID:                   "test-native",
		DisplayName:          "Native",
		NetworkPolicy:        provider.NetPolicySystem,
		NativeResponses:      true,
		DefaultResponsesPath: "/responses",
	})
	env := testEnv("test-native", srv.Client())

	rawReq := []byte(`{"model":"m1","stream":true,"x_vendor_meta":"keep-me"}`)
	resp, err := p.ResponsesPassthrough(context.Background(), env, rawReq, "m1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if gotModel != "m1" {
		t.Fatalf("model = %q", gotModel)
	}
	if gotMeta != "keep-me" {
		t.Fatalf("非标字段丢失: %q", gotMeta)
	}
	if gotAuth != "Bearer tk" {
		t.Fatalf("auth = %q", gotAuth)
	}
}

func TestAutoModelPassthrough(t *testing.T) {
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {}\n\n")
	}))
	defer srv.Close()

	loadTestSpec(t, fmt.Sprintf(`{
	  "provider_id": "test-auto",
	  "spec_version": "1",
	  "base_url": %q,
	  "endpoints": {"responses": "/responses"}
	}`, srv.URL))

	p := New(Profile{
		ID:                   "test-auto",
		DisplayName:          "Auto",
		NetworkPolicy:        provider.NetPolicySystem,
		NativeResponses:      true,
		DefaultResponsesPath: "/responses",
		AutoModel:            true,
	})
	env := testEnv("test-auto", srv.Client())
	env.Credentials["model"] = "gpt-6.1-sol"

	_, err := p.ResponsesPassthrough(context.Background(), env,
		[]byte(`{"model":"test-auto/auto","stream":true}`), autoModelUpstream)
	if err != nil {
		t.Fatal(err)
	}
	if gotModel != "gpt-6.1-sol" {
		t.Fatalf("auto model = %q", gotModel)
	}

	if m := p.Manifest(); len(m.Models) != 1 || m.Models[0].ID != "test-auto/auto" ||
		m.Models[0].UpstreamModel != autoModelUpstream {
		t.Fatalf("Manifest 未暴露 auto 模型: %+v", m.Models)
	}
}

func TestParseTOMLFlat(t *testing.T) {
	raw := []byte(`
model = "gpt-6.1-sol" # top level
[model_providers.custom]
name = "WawAPI"
base_url = "https://example.com"
requires_openai_auth = true
experimental_bearer_token = "token-with#hash"
[other]
key = "value"
`)
	flat := parseTOMLFlat(raw)
	cases := map[string]string{
		"model":                                            "gpt-6.1-sol",
		"model_providers.custom.name":                      "WawAPI",
		"model_providers.custom.base_url":                  "https://example.com",
		"model_providers.custom.requires_openai_auth":      "true",
		"model_providers.custom.experimental_bearer_token": "token-with#hash",
		"other.key":                                        "value",
	}
	for k, want := range cases {
		if got := flat[k]; got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestBuiltinLocalImportRules(t *testing.T) {
	if cfg, ok := builtinLocalImport("codex"); !ok || cfg.Format != "toml" {
		t.Fatalf("codex 内置规则缺失: %+v", cfg)
	}
	oc, ok := builtinLocalImport("opencode")
	if !ok || oc.Format != "json" || oc.Credentials["api_key"] != "opencode.key" {
		t.Fatalf("opencode 内置规则缺失: %+v", oc)
	}
	goCfg, ok := builtinLocalImport("opencode-go")
	if !ok || goCfg.Format != "json" || goCfg.Credentials["api_key"] != "opencode-go.key" || goCfg.Label != "OpenCode Go" {
		t.Fatalf("opencode-go 内置规则缺失: %+v", goCfg)
	}
	if oc.Label != "OpenCode Zen" {
		t.Fatalf("opencode 展示名 = %q", oc.Label)
	}
	if _, ok := builtinLocalImport("trae-intl"); ok {
		t.Fatal("未验证的渠道不应有内置规则")
	}
}

// TestDefaultBaseURLAndModels 覆盖"渠道自带端点与模型清单"的路径：
// 账号只带 api_key、没有 base_url、也没有 spec 时，仍应能直连默认端点。
func TestDefaultBaseURLAndModels(t *testing.T) {
	var gotPath, gotAuth, gotModel, gotSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotSession = r.Header.Get("x-opencode-session")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	spec.SetDefault(spec.NewStore())
	t.Cleanup(func() { spec.SetDefault(spec.NewStore()) })

	p := New(Profile{
		ID:              "test-default-base",
		DisplayName:     "Default Base",
		NetworkPolicy:   provider.NetPolicySystem,
		DefaultBaseURL:  srv.URL,
		DefaultChatPath: "/chat/completions",
		SessionHeader:   "x-opencode-session",
		DefaultModels:   []string{"m1"},
	})
	env := provider.NewAccountEnv("acc", "test-default-base")
	env.Credentials = map[string]string{"api_key": "oc_sk_test"}
	env.HTTPClient = srv.Client()

	req := &ir.UnifiedChatRequest{
		Model:         "test-default-base/m1",
		UpstreamModel: "m1",
		Stream:        true,
		SessionKey:    "sess-123",
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for ev := range ch {
		if ev.Type == ir.EventTextDelta {
			text += ev.TextDelta
		}
	}
	if text != "OK" {
		t.Fatalf("文本 = %q", text)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("路径 = %q", gotPath)
	}
	if gotAuth != "Bearer oc_sk_test" {
		t.Fatalf("鉴权头 = %q", gotAuth)
	}
	if gotModel != "m1" {
		t.Fatalf("模型 = %q", gotModel)
	}
	if gotSession != "sess-123" {
		t.Fatalf("会话头 = %q", gotSession)
	}

	if m := p.Manifest(); len(m.Models) != 1 || m.Models[0].ID != "test-default-base/m1" {
		t.Fatalf("模型清单 = %+v", m.Models)
	}
}

// TestOpenCodeProfile 校验 OpenCode 渠道的真实端点与模型清单已内置，
// 不依赖 spec 文件即可出现在 /v1/models。
func TestOpenCodeProfile(t *testing.T) {
	p, ok := provider.Get("opencode")
	if !ok {
		t.Fatal("opencode 未注册")
	}
	prof := p.(*Provider).Profile()
	if prof.DefaultBaseURL != "https://opencode.ai/zen/v1" || prof.DefaultChatPath != "/chat/completions" {
		t.Fatalf("opencode 端点 = %q %q", prof.DefaultBaseURL, prof.DefaultChatPath)
	}
	m := p.Manifest()
	if len(m.Models) < 10 {
		t.Fatalf("opencode 模型清单过少: %d", len(m.Models))
	}
	found := false
	for _, model := range m.Models {
		if model.ID == "opencode/big-pickle" && model.UpstreamModel == "big-pickle" {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺少 opencode/big-pickle: %+v", m.Models)
	}
	if !hasAPIKeyField(m.AuthFormSchema) {
		t.Fatalf("opencode 手动表单应为 API Key: %+v", m.AuthFormSchema)
	}
}

// TestOpenCodeGoProfile 覆盖 Go 订阅：端点、会话头、额度类型与 28 个模型。
func TestOpenCodeGoProfile(t *testing.T) {
	p, ok := provider.Get("opencode-go")
	if !ok {
		t.Fatal("opencode-go 未注册")
	}
	prof := p.(*Provider).Profile()
	// canonical 端点：官方命中率与鉴权头都比旧的 /zen/go 更合适（见
	// opencode_endpoint_test.go 的说明）。
	if prof.DefaultBaseURL != "https://opencode.ai/inference/go/openai/v1" || prof.DefaultChatPath != "/chat/completions" {
		t.Fatalf("opencode-go 端点 = %q %q", prof.DefaultBaseURL, prof.DefaultChatPath)
	}
	if prof.SessionHeader != "x-opencode-session" || prof.QuotaKind != "opencode_go" {
		t.Fatalf("opencode-go 会话头/额度类型 = %q %q", prof.SessionHeader, prof.QuotaKind)
	}
	m := p.Manifest()
	if len(m.Models) != 28 {
		t.Fatalf("opencode-go 模型数 = %d", len(m.Models))
	}
	seen := map[string]bool{}
	for _, model := range m.Models {
		seen[model.ID] = true
	}
	for _, want := range []string{"opencode-go/deepseek-v4.1-flash", "opencode-go/kimi-k3", "opencode-go/gemini-4-argon"} {
		if !seen[want] {
			t.Fatalf("缺少 %s: %+v", want, m.Models)
		}
	}
	if !hasAPIKeyField(m.AuthFormSchema) {
		t.Fatalf("opencode-go 手动表单应为 API Key: %+v", m.AuthFormSchema)
	}
}

func hasAPIKeyField(form []provider.FormFieldSchema) bool {
	for _, f := range form {
		if f.Key == "api_key" && f.Required {
			return true
		}
	}
	return false
}

func TestParseUnixValueForms(t *testing.T) {
	if got := parseInt64(float64(1700000000000)); got != 1700000000 {
		t.Fatalf("毫秒归一化 = %d", got)
	}
	if got := parseInt64("2030-01-02T03:04:05Z"); got == 0 {
		t.Fatal("RFC3339 未解析")
	}
	if got := parseInt64("1700000000"); got != 1700000000 {
		t.Fatalf("数字字符串 = %d", got)
	}
}

func TestListModelsFromUpstream(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":[{"id":"m1"},{"id":"m2"},{"id":"m1"}]}`)
	}))
	defer srv.Close()
	spec.SetDefault(spec.NewStore())
	t.Cleanup(func() { spec.SetDefault(spec.NewStore()) })

	p := New(Profile{
		ID: "test-models", NetworkPolicy: provider.NetPolicySystem,
		DefaultBaseURL: srv.URL, ModelListPath: "/models",
	})
	env := provider.NewAccountEnv("acc", "test-models")
	env.Credentials = map[string]string{"api_key": "oc_sk_test"}
	env.HTTPClient = srv.Client()

	if !p.SupportsModelList(env) {
		t.Fatal("配置了 ModelListPath 应支持模型列表")
	}
	ids, err := p.ListModels(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "m1" || ids[1] != "m2" {
		t.Fatalf("ids = %v", ids)
	}
	if gotPath != "/models" || gotAuth != "Bearer oc_sk_test" {
		t.Fatalf("path=%q auth=%q", gotPath, gotAuth)
	}
	if New(Profile{ID: "no-models"}).SupportsModelList(env) {
		t.Fatal("没有 ModelListPath 的渠道不应支持实时模型目录")
	}
}
