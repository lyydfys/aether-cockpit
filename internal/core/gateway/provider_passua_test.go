package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 保存 / 回显 passUserAgent。
func TestProviderPassUASaveEcho(t *testing.T) {
	g := headersGateway(t)
	if rec := saveHeaders(t, g, `{"id":"mock-native","passUserAgent":true}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	if v := providerViewOf(t, g, "mock-native")["passUserAgent"]; v != true {
		t.Fatalf("回显 passUserAgent = %v", v)
	}
	if rec := saveHeaders(t, g, `{"id":"mock-native","passUserAgent":false}`); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d", rec.Code)
	}
	if v := providerViewOf(t, g, "mock-native")["passUserAgent"]; v != false {
		t.Fatalf("关闭后回显 = %v", v)
	}
}

// 包装层：开关打开时用 ctx 里的下游 UA 覆盖上游 UA；关闭时保留渠道自己的 UA。
func TestProviderPassUAOverridesUpstreamUA(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.UserAgent()
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	g := headersGateway(t)
	env := g.pool.ListByProvider("mock-native")[0]
	env.SetHTTPClient(srv.Client())

	send := func(ctx context.Context) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		req.Header.Set("User-Agent", "provider-own-ua/1.0")
		resp, err := env.HTTPClient.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		resp.Body.Close()
	}

	// 关闭：保持渠道自己的 UA。
	g.applyProviderTransport(env)
	send(withDownstreamUA(context.Background(), "agent/9.9"))
	if seen != "provider-own-ua/1.0" {
		t.Fatalf("关闭时不应覆盖，got %q", seen)
	}

	// 打开：下游 UA 覆盖。
	if rec := saveHeaders(t, g, `{"id":"mock-native","passUserAgent":true}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d", rec.Code)
	}
	g.applyProviderTransport(env)
	send(withDownstreamUA(context.Background(), "agent/9.9"))
	if seen != "agent/9.9" {
		t.Fatalf("打开时应透传下游 UA，got %q", seen)
	}

	// 没有下游 UA（网关自调用）：保持原样。
	send(context.Background())
	if seen != "provider-own-ua/1.0" {
		t.Fatalf("无下游 UA 时应保持原 UA，got %q", seen)
	}
}

// 端到端：下游带 User-Agent 请求，开关打开时上游收到的是 Agent 的 UA。
func TestProviderPassUAEndToEnd(t *testing.T) {
	var seen string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.UserAgent()
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-ua","object":"chat.completion","created":1,"model":"x",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer upstream.Close()

	g := New(Config{
		MaxAttempts:             2,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:              10,
	})
	env := provider.NewAccountEnv("acc-ua", "openai-compat")
	env.Credentials = map[string]string{"base_url": upstream.URL + "/v1", "api_key": "sk-x"}
	env.SetHTTPClient(upstream.Client())
	g.Pool().Add(env)
	g.models.Rebuild()
	if rec := saveHeaders(t, g, `{"id":"openai-compat","passUserAgent":true}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	g.applyProviderTransport(env)

	srv := httptest.NewServer(g)
	defer srv.Close()
	body := `{"model":"openai-compat/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":false}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Claude-Code/2.1.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d: %s", resp.StatusCode, raw)
	}
	if seen != "Claude-Code/2.1.0" {
		t.Fatalf("上游应收到 Agent 的 UA，got %q", seen)
	}
}
