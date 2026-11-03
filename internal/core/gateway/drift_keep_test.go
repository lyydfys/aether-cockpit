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

// "保持当前设置"必须真的压住漂移提示，直到配置再次变化。
//
// 之前这个动作只回一次 state：前端清掉提示后，下次刷新 /api/drift 又冒出来，
// 用户点十次也没用，等于没有这个能力。
func TestDriftKeepSuppressesUntilConfigChanges(t *testing.T) {
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
	_ = g.clientsManager()
	g.clientMgr = clients.NewWithHome(home)

	driftOf := func() map[string]any {
		req := httptest.NewRequest("GET", "/api/drift", nil)
		req.Host = "127.0.0.1:7860"
		rec := httptest.NewRecorder()
		g.handleDrift(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	if _, ok := driftOf()["claude_code"]; !ok {
		t.Fatal("前置条件：应检测到漂移")
	}

	keep := httptest.NewRequest("POST", "/api/agents/keep/claude_code", strings.NewReader("{}"))
	keep.Host = "127.0.0.1:7860"
	keep.SetPathValue("action", "keep")
	keep.SetPathValue("id", "claude_code")
	rec := httptest.NewRecorder()
	g.handleAgentAction(rec, keep)
	if rec.Code != 200 {
		t.Fatalf("keep status=%d body=%s", rec.Code, rec.Body.String())
	}

	if _, ok := driftOf()["claude_code"]; ok {
		t.Fatal("keep 之后不该再报同一条漂移")
	}

	// 配置再次变化 → 摘要变了 → 漂移重新出现。
	changed := `{"env":{"ANTHROPIC_BASE_URL":"http://another.example/v1","ANTHROPIC_AUTH_TOKEN":"old"}}`
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := driftOf()["claude_code"]; !ok {
		t.Fatal("配置再次变化后应重新报漂移")
	}
}
