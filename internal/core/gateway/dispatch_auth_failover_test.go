package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 第一个账号返回 401（凭证失效）时，应标记它并换下一个账号，而不是
// 把 401 直接甩给客户端。
func TestDispatchFailsOverOnAuthError(t *testing.T) {
	var badHits int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&badHits, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"authentication_error"}}`))
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-auth","object":"chat.completion","created":1,"model":"x",
			"choices":[{"index":0,"message":{"role":"assistant","content":"good-account"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer good.Close()

	g := New(Config{
		MaxAttempts:             4,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:              20,
	})
	a := provider.NewAccountEnv("acc-bad", "openai-compat")
	a.Credentials = map[string]string{"base_url": bad.URL + "/v1", "api_key": "sk-bad"}
	g.Pool().Add(a)
	b := provider.NewAccountEnv("acc-good", "openai-compat")
	b.Credentials = map[string]string{"base_url": good.URL + "/v1", "api_key": "sk-good"}
	g.Pool().Add(b)
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
		t.Fatalf("401 后应换账号成功，status=%d: %s", resp.StatusCode, raw)
	}
	if !strings.Contains(string(raw), "good-account") {
		t.Fatalf("应由第二个账号应答: %s", raw)
	}
	if atomic.LoadInt32(&badHits) == 0 {
		t.Fatal("坏账号应至少被尝试一次")
	}
	// 失效账号被标记 invalid 后，下一次请求不应再命中它。
	before := atomic.LoadInt32(&badHits)
	resp2, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST 2: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("第二次应直接成功，status=%d", resp2.StatusCode)
	}
	if atomic.LoadInt32(&badHits) != before {
		t.Fatalf("失效账号不应再被尝试（hits %d → %d）", before, atomic.LoadInt32(&badHits))
	}
}

// 唯一账号凭证失效时，返回的仍是 401（不被"没有可用账号"掩盖）。
func TestDispatchAuthErrorSurfacesWhenOnlyAccountInvalid(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"authentication_error"}}`))
	}))
	defer bad.Close()

	g := New(Config{
		MaxAttempts:             3,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:              10,
	})
	a := provider.NewAccountEnv("acc-bad", "openai-compat")
	a.Credentials = map[string]string{"base_url": bad.URL + "/v1", "api_key": "sk-bad"}
	g.Pool().Add(a)
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
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status=%d: %s", resp.StatusCode, raw)
	}
	low := strings.ToLower(string(raw))
	if !strings.Contains(low, "401") && !strings.Contains(low, "authentication") && !strings.Contains(low, "api key") && !strings.Contains(low, "auth") {
		t.Fatalf("应把上游的认证错误透出，而不是含糊的 no_account: %s", raw)
	}
}
