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
)

// 第一个账号"额度用尽"（上游 400 + 额度文案）时，必须在**首字节之前**换到
// 第二个账号，而不是把 429 抛给客户端。
//
// 这是用户实际遇到的场景：一个号没额度，请求直接断。
func TestDispatchFailsOverOnQuotaExhausted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-ok","object":"chat.completion","created":1,"model":"x",
			"choices":[{"index":0,"message":{"role":"assistant","content":"from-good-account"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer upstream.Close()

	// 额度耗尽的上游：返回 400 + agent_router 文案（与 Qoder 实测一致）。
	exhausted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"400","message":"[FAIL]node:agent_router msg:None flow nodes found for router agent_router"}`))
	}))
	defer exhausted.Close()

	g := New(Config{
		MaxAttempts:             4,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:              20,
	})
	empty := provider.NewAccountEnv("acc-empty", "openai-compat")
	empty.Credentials = map[string]string{"base_url": exhausted.URL, "api_key": "sk-empty"}
	g.Pool().Add(empty)
	good := provider.NewAccountEnv("acc-good", "openai-compat")
	good.Credentials = map[string]string{"base_url": upstream.URL, "api_key": "sk-good"}
	g.Pool().Add(good)
	g.models.Rebuild()

	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"model":"openai-compat/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":false}`
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("额度耗尽的账号应被跳过并通过第二个账号成功，status=%d: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "from-good-account") {
		t.Fatalf("应由第二个账号应答：%s", raw)
	}
}
