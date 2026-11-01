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

// 兜底链的保存 / 回显 / 清除。
func TestProviderFallbackSaveEcho(t *testing.T) {
	g := headersGateway(t)
	rec := saveHeaders(t, g, `{"id":"mock-native","fallback":["openai-compat/gpt-4o","antigravity/gemini-3-flash"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	view := providerViewOf(t, g, "mock-native")
	fb, _ := view["fallback"].([]any)
	if len(fb) != 2 || fb[0] != "openai-compat/gpt-4o" {
		t.Fatalf("回显 fallback = %v", view["fallback"])
	}
	if rec := saveHeaders(t, g, `{"id":"mock-native","fallback":[]}`); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d", rec.Code)
	}
	if got := g.providerFallback("mock-native"); len(got) != 0 {
		t.Fatalf("清空后仍有兜底: %v", got)
	}
}

// 主渠道接不住时，按渠道级兜底链换到能用的模型（端到端）。
func TestProviderFallbackUsedWhenPrimaryFails(t *testing.T) {
	g := New(Config{
		MaxAttempts:             4,
		FirstByteTimeout:        3 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:              20,
	})
	// 主渠道：指向一个必然连不上的地址。
	dead := provider.NewAccountEnv("acc-dead", "openai-compat")
	dead.Credentials = map[string]string{"base_url": "http://127.0.0.1:1/v1", "api_key": "sk-x"}
	g.Pool().Add(dead)
	// 兜底渠道：mock-native，离线可用。
	mock := provider.NewAccountEnv("acc-mock", "mock-native")
	g.Pool().Add(mock)
	g.models.Rebuild()

	if err := g.setProviderFallback("openai-compat", []string{"mock-native/echo-pro"}); err != nil {
		t.Fatalf("setProviderFallback: %v", err)
	}

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
		t.Fatalf("status = %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("解析响应: %v (%s)", err, raw)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		t.Fatalf("兜底响应为空: %s", raw)
	}
	// 日志里这次请求应落在 mock-native（证明走的是兜底而非主渠道）。
	var hit string
	for _, rec := range g.logs.Recent(5) {
		hit = rec.ProviderID
	}
	if hit != "mock-native" {
		t.Fatalf("兜底未生效，最后记录渠道 = %q", hit)
	}
}
