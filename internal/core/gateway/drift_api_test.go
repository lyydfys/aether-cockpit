package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/clients"
)

func driftGet(t *testing.T, g *Gateway) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handleDrift(rec, httptest.NewRequest(http.MethodGet, "/api/drift", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("drift = %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("非 JSON: %v", err)
	}
	return out
}

func TestDriftDetectsRewrittenClaudeConfig(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 一个"被别的工具改回官方地址"的 Claude 配置。
	raw := `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com","ANTHROPIC_AUTH_TOKEN":"sk-ant-official","ANTHROPIC_MODEL":"claude-sonnet-4"}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.clientsOnce.Do(func() {})
	g.clientMgr = clients.NewWithHome(home)

	out := driftGet(t, g)
	d, ok := out["claude_code"].(map[string]any)
	if !ok {
		t.Fatalf("应检测到 claude_code 漂移: %v", out)
	}
	if d["kind"] != "unwired" || d["field"] != "model" {
		t.Fatalf("drift = %v", d)
	}
	if !strings.Contains(d["detail"].(string), "settings.json") {
		t.Fatalf("detail 未指向配置路径: %v", d["detail"])
	}
	if d["want"] == "" {
		t.Fatalf("want 不应为空: %v", d)
	}
}

func TestDriftCleanWhenNothingDetected(t *testing.T) {
	home := t.TempDir() // 没有任何客户端配置
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.clientsOnce.Do(func() {})
	g.clientMgr = clients.NewWithHome(home)

	if out := driftGet(t, g); len(out) != 0 {
		t.Fatalf("没接入任何客户端时不应有漂移: %v", out)
	}
}

func TestDriftFromChangesDetail(t *testing.T) {
	d := driftFromChanges([]clients.PreviewChange{{
		Path: "/home/u/.claude/settings.json",
		Lines: []clients.DiffLine{
			{Op: "-", Text: "  ANTHROPIC_BASE_URL=https://api.anthropic.com"},
			{Op: "+", Text: "  ANTHROPIC_BASE_URL=http://127.0.0.1:7860"},
		},
	}}, "mock-native/echo-pro")
	if !strings.Contains(d["detail"].(string), "ANTHROPIC_BASE_URL=https://api.anthropic.com") {
		t.Fatalf("detail 应带第一条差异: %v", d["detail"])
	}
	if d["want"] != "mock-native/echo-pro" {
		t.Fatalf("want = %v", d["want"])
	}
}
