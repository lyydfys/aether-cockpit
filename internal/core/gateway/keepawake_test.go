package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeepAwakeSettingPersists(t *testing.T) {
	if err := setKeepAwake(true); err != nil {
		t.Fatalf("SetThreadExecutionState: %v", err)
	}
	defer func() { _ = setKeepAwake(false) }()

	path := filepath.Join(t.TempDir(), "settings.json")
	g := New(Config{SettingsPath: path, UsagePath: "-"})

	post := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/settings", strings.NewReader(body))
		g.handleUpdateSettings(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("settings = %d %s", rec.Code, rec.Body.String())
		}
	}
	post(`{"keep_awake":true}`)
	if !g.settings.KeepAwake {
		raw, _ := os.ReadFile(path)
		t.Fatalf("keep_awake 未生效; saved=%s", raw)
	}
	g2 := New(Config{SettingsPath: path, UsagePath: "-"})
	if !g2.settings.KeepAwake {
		t.Fatal("keep_awake 未持久化")
	}
	post(`{"keep_awake":false}`)
	if g.settings.KeepAwake {
		t.Fatal("keep_awake 未关闭")
	}
}
