package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"

	// 渠道实现要注册进 registry，模型目录才能解析出绑定。
	_ "aether-cockpit/internal/providers/cursor"
	_ "aether-cockpit/internal/providers/openaicompat"
)

func newNonChatTestGateway(t *testing.T) *Gateway {
	t.Helper()
	return New(Config{
		MaxAttempts:             4,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:               "-",
		MaxRecords:              20,
	})
}

func postChat(t *testing.T, srv *httptest.Server, model string) (int, string) {
	t.Helper()
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"stream":false}`
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// 只有"非 Chat 渠道"（cursor 这类只有账号/额度的通道）时，必须返回
// 400 capability_unsupported 并点名渠道，而不是笼统的「所有候选渠道均不可用」。
func TestOnlyNonChatChannelsReturnsCapabilityUnsupported(t *testing.T) {
	g := newNonChatTestGateway(t)
	// cursor 声明了 cursor/auto 模型；给它一个账号，模型目录就会产生绑定。
	g.Pool().Add(provider.NewAccountEnv("acc-cursor", "cursor"))
	g.models.Rebuild()

	srv := httptest.NewServer(g)
	defer srv.Close()

	status, body := postChat(t, srv, "cursor/auto")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d，期望 400: %s", status, body)
	}
	if !strings.Contains(body, "capability_unsupported") {
		t.Fatalf("响应缺少 capability_unsupported: %s", body)
	}
	if !strings.Contains(body, "渠道 cursor 不支持对话") {
		t.Fatalf("错误文案应点名 cursor: %s", body)
	}
}

// 模型完全不存在（没有任何候选渠道）时，仍保留 503 no_provider。
func TestUnknownModelStillReturnsNoProvider(t *testing.T) {
	g := newNonChatTestGateway(t)
	srv := httptest.NewServer(g)
	defer srv.Close()

	status, body := postChat(t, srv, "no-such-provider/no-such-model")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d，期望 503: %s", status, body)
	}
	if !strings.Contains(body, "no_provider") {
		t.Fatalf("响应缺少 no_provider: %s", body)
	}
}

// 有真正支持 Chat 的渠道时，请求照常成功（不能被新错误分支拦住）。
func TestChatChannelStillServesAfterNonChatError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-nonchat","object":"chat.completion","created":1,"model":"gpt-4o",` +
			`"choices":[{"index":0,"message":{"role":"assistant","content":"still-works"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer upstream.Close()

	g := newNonChatTestGateway(t)
	env := provider.NewAccountEnv("acc-openai", "openai-compat")
	env.Credentials = map[string]string{"base_url": upstream.URL + "/v1", "api_key": "sk-test"}
	g.Pool().Add(env)
	// 同时挂一个非 Chat 账号：它应被跳过，而不是把请求带偏。
	g.Pool().Add(provider.NewAccountEnv("acc-cursor", "cursor"))
	g.models.Rebuild()

	srv := httptest.NewServer(g)
	defer srv.Close()

	status, body := postChat(t, srv, "openai-compat/gpt-4o")
	if status != http.StatusOK {
		t.Fatalf("status = %d，期望 200: %s", status, body)
	}
	if !strings.Contains(body, "still-works") {
		t.Fatalf("应由 Chat 渠道应答: %s", body)
	}
}

// 文案：去重、最多列 3 个、其余「等 N 个」。
func TestNonChatChannelsMessage(t *testing.T) {
	cases := []struct {
		name string
		ids  []string
		want string
	}{
		{"单个", []string{"cursor"}, "渠道 cursor 不支持对话（仅账号/额度）"},
		{"三个", []string{"cursor", "windsurf", "zed"}, "渠道 cursor、windsurf、zed 不支持对话（仅账号/额度）"},
		{"超过三个", []string{"cursor", "windsurf", "zed", "typesafe", "codex"}, "渠道 cursor、windsurf、zed 等 5 个不支持对话（仅账号/额度）"},
		{"去重保序", []string{"cursor", "cursor", "zed"}, "渠道 cursor、zed 不支持对话（仅账号/额度）"},
		{"空串忽略", []string{"", "cursor", ""}, "渠道 cursor 不支持对话（仅账号/额度）"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nonChatChannelsMessage(tc.ids); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
