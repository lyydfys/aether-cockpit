package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 单渠道多账号：第一个账号的地址接不上时，必须换下一个账号重试，
// 而不是把错误直接甩给客户端。
func TestDispatchFailsOverAcrossAccounts(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-fo","object":"chat.completion","created":1,"model":"x",
			"choices":[{"index":0,"message":{"role":"assistant","content":"second-account"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer upstream.Close()

	g := New(Config{
		MaxAttempts:             4,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:              20,
	})
	dead := provider.NewAccountEnv("acc-dead", "openai-compat")
	dead.Credentials = map[string]string{"base_url": "http://127.0.0.1:1/v1", "api_key": "sk-x"}
	g.Pool().Add(dead)
	alive := provider.NewAccountEnv("acc-alive", "openai-compat")
	alive.Credentials = map[string]string{"base_url": upstream.URL, "api_key": "sk-y"}
	g.Pool().Add(alive)
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
		t.Fatalf("应换到第二个账号成功，status=%d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("解析: %v (%s)", err, raw)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content != "second-account" {
		t.Fatalf("应由第二个账号应答: %s", raw)
	}
}

// 尝试额度用尽就停下（不要把失败账号无限重排）。
func TestDispatchFailoverRespectsMaxAttempts(t *testing.T) {
	g := New(Config{
		MaxAttempts:             1,
		FirstByteTimeout:        2 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:              10,
	})
	dead := provider.NewAccountEnv("acc-dead", "openai-compat")
	dead.Credentials = map[string]string{"base_url": "http://127.0.0.1:1/v1", "api_key": "sk-x"}
	g.Pool().Add(dead)
	alive := provider.NewAccountEnv("acc-alive", "openai-compat")
	alive.Credentials = map[string]string{"base_url": "http://127.0.0.1:1/v1", "api_key": "sk-y"}
	g.Pool().Add(alive)
	g.models.Rebuild()

	srv := httptest.NewServer(g)
	defer srv.Close()
	body := `{"model":"openai-compat/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":false}`
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("MaxAttempts=1 时不该成功（两个地址都接不上）")
	}
}
