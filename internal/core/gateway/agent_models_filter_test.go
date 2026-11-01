package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 模型清单只列"已接入账号"的渠道：测试统一带一个 mock-native 账号。
func modelsGateway(t *testing.T) *Gateway {
	t.Helper()
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-", MaxRecords: 10, StateTTL: time.Minute, FirstByteTimeout: time.Second, MaxAttempts: 1})
	env := provider.NewAccountEnv("acc-models", "mock-native")
	env.HTTPClient = &http.Client{Timeout: time.Second}
	g.Pool().Add(env)
	return g
}

func TestModelsFilteredPerAgent(t *testing.T) {
	g := modelsGateway(t)
	if err := g.saveAgentModelPrefs("opencode", map[string]any{"hidden": []any{"mock-native/echo-pro"}}); err != nil {
		t.Fatal(err)
	}

	list := func(ua string) map[string]bool {
		req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		req.Header.Set("User-Agent", ua)
		rec := httptest.NewRecorder()
		g.handleModels(rec, req)
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, d := range out.Data {
			ids[d.ID] = true
		}
		return ids
	}

	if list("opencode/0.1.0")["mock-native/echo-pro"] {
		t.Fatal("opencode 隐藏的模型仍在 /v1/models 里")
	}
	if !list("claude-cli/2.1.0")["mock-native/echo-pro"] {
		t.Fatal("其它 Agent 不应受影响")
	}
	if !list("")["mock-native/echo-pro"] {
		t.Fatal("无 UA 时不应隐藏")
	}

	// Gemini 清单同样生效。
	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	req.Header.Set("User-Agent", "opencode/0.1.0")
	rec := httptest.NewRecorder()
	g.handleGeminiModels(rec, req)
	var gout struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &gout)
	for _, m := range gout.Models {
		if m.Name == "models/mock-native/echo-pro" {
			t.Fatal("Gemini 清单仍包含被隐藏的模型")
		}
	}
}

func TestModelsOrderedPerAgent(t *testing.T) {
	g := modelsGateway(t)
	ids := []string{"mock-native/echo-pro", "mock-weak/echo-lite"}
	if err := g.saveAgentModelPrefs("opencode", map[string]any{"order": []any{ids[1], ids[0]}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("User-Agent", "opencode/0.1.0")
	rec := httptest.NewRecorder()
	g.handleModels(rec, req)
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	pos := map[string]int{}
	for i, d := range out.Data {
		pos[d.ID] = i
	}
	if pos[ids[1]] > pos[ids[0]] {
		t.Fatalf("order not applied: %v", pos)
	}
}

func TestAgentModelOptionsRespectHideAndOrder(t *testing.T) {
	g := modelsGateway(t)
	if err := g.saveAgentModelPrefs("claude_code", map[string]any{
		"hidden": []any{"mock-native/echo-pro"},
		"order":  []any{"mock-weak/echo-lite"},
	}); err != nil {
		t.Fatal(err)
	}
	opts := g.agentModelOptions("claude_code")
	if len(opts) == 0 {
		t.Fatal("options 为空")
	}
	seen := map[string]int{}
	for i, o := range opts {
		seen[o.Value] = i
	}
	if _, ok := seen["mock-native/echo-pro"]; ok {
		t.Fatal("隐藏模型仍在 picker 里")
	}
	if seen["mock-weak/echo-lite"] != 0 {
		t.Fatalf("排序未生效: %v", seen)
	}
}
