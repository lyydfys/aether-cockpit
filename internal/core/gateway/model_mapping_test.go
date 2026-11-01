package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/provider"
)

func TestAccountMappingAppliedToUpstream(t *testing.T) {
	var mu sync.Mutex
	var models []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		models = append(models, body.Model)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"x",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer upstream.Close()

	g := New(Config{
		MaxAttempts:             1,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		MaxRecords:              20,
	})
	env := provider.NewAccountEnv("acc-map", "openai-compat")
	env.Credentials = map[string]string{"base_url": upstream.URL, "api_key": "sk-x"}
	env.HTTPClient = upstream.Client()
	env.SetModelMapping(map[string]string{"gpt-4o": "mapped-upstream"})
	g.Pool().Add(env)
	g.models.Rebuild()

	gateway := httptest.NewServer(g)
	defer gateway.Close()

	send := func() {
		body := `{"model":"openai-compat/gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":false}`
		resp, err := http.Post(gateway.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.StatusCode, raw)
		}
	}
	send()
	mu.Lock()
	got := append([]string(nil), models...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "mapped-upstream" {
		t.Fatalf("上游收到模型 = %v, want [mapped-upstream]", got)
	}

	// 清除映射后恢复原名。
	env.SetModelMapping(nil)
	send()
	mu.Lock()
	got = append([]string(nil), models...)
	mu.Unlock()
	if len(got) != 2 || got[1] != "gpt-4o" {
		t.Fatalf("清除映射后 = %v", got)
	}
}

func TestAccountMappingAPI(t *testing.T) {
	g := &Gateway{
		pool:         pool.New(),
		settingsPath: filepath.Join(t.TempDir(), "settings.json"),
		models:       NewModelRouter(),
	}
	env := provider.NewAccountEnv("acc-api-map", "openai-compat")
	g.pool.Add(env)

	req := httptest.NewRequest(http.MethodPost, "/api/provider/accountmapping",
		strings.NewReader(`{"id":"openai-compat","account":"acc-api-map","mapping":{"gpt-4o":"relay-gpt"}}`))
	rec := httptest.NewRecorder()
	g.handleProviderAccountMapping(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if env.MapModel("gpt-4o") != "relay-gpt" {
		t.Fatalf("映射未生效: %+v", env.ModelMapping())
	}
	var payload struct {
		Providers []struct {
			ID                  string                       `json:"id"`
			AccountModelMapping map[string]map[string]string `json:"accountModelMapping"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析 providers: %v", err)
	}
	found := false
	for _, p := range payload.Providers {
		if p.ID == "openai-compat" && p.AccountModelMapping["acc-api-map"]["gpt-4o"] == "relay-gpt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("providers 未带 accountModelMapping: %s", rec.Body.String())
	}
}
