package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/clients"
)

// TestDriftApplyRewritesConfig 锁住 P2：检测到漂移后能一键写回，
// 而不是只读地提示“配置被改过”。
func TestDriftApplyRewritesConfig(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	original := `{"env":{"ANTHROPIC_BASE_URL":"http://old.example/v1","ANTHROPIC_AUTH_TOKEN":"old","ANTHROPIC_MODEL":"mock-native/echo-pro"}}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	g := New(Config{
		Addr:         "127.0.0.1:7860",
		SettingsPath: filepath.Join(t.TempDir(), "settings.json"),
		UsagePath:    "-",
		MaxRecords:   20,
	})
	_ = g.clientsManager() // 先把 Once 走完，再替换成测试用 home
	g.clientMgr = clients.NewWithHome(home)

	driftReq := func() map[string]any {
		req := httptest.NewRequest("GET", "/api/drift", nil)
		req.Host = "127.0.0.1:7860"
		rec := httptest.NewRecorder()
		g.handleDrift(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	if _, ok := driftReq()["claude_code"]; !ok {
		t.Fatal("应检测到 Claude Code 配置漂移")
	}

	req := httptest.NewRequest("POST", "/api/drift/apply", strings.NewReader(`{"ids":["claude_code"]}`))
	req.Host = "127.0.0.1:7860"
	rec := httptest.NewRecorder()
	g.handleDriftApply(rec, req)
	if rec.Code != 200 {
		t.Fatalf("apply status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Applied []string          `json:"applied"`
		Errors  map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Applied) != 1 || out.Applied[0] != "claude_code" {
		t.Fatalf("applied=%v errors=%v", out.Applied, out.Errors)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "old.example") {
		t.Fatalf("配置没有被写回: %s", raw)
	}
	if !strings.Contains(string(raw), "127.0.0.1:7860") {
		t.Fatalf("写回后的地址不对: %s", raw)
	}
}