package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionsManageCarriesWSL(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-home-wsl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"/home/wsl/app","message":{"content":"managed WSL"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "wsl-mng-1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AETHER_WSL_HOMES", home)
	t.Setenv("AETHER_WSL_DISTRO", "Ubuntu-22.04")

	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sessions/manage", g.handleSessionsManage)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/sessions/manage?agent=claude-code", nil))
	if rec.Code != 200 {
		t.Fatalf("manage = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, s := range out.Sessions {
		if s["id"] == "wsl-mng-1" {
			if s["wsl"] != "Ubuntu-22.04" {
				t.Fatalf("wsl = %v", s["wsl"])
			}
			return
		}
	}
	t.Fatalf("manage 未包含 WSL 会话: %v", out.Sessions)
}
