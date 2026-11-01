package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/appstart"
)

func newAutoStartGateway(t *testing.T, desktop bool) *Gateway {
	t.Helper()
	return New(Config{
		MaxAttempts:      1,
		FirstByteTimeout: time.Second,
		StateTTL:         time.Minute,
		MaxRecords:       10,
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		DesktopApp:       desktop,
	})
}

func getSettingsView(t *testing.T, g *Gateway) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("settings 不是 JSON: %v", err)
	}
	return out
}

func TestAutoStartUnsupportedInHeadless(t *testing.T) {
	g := newAutoStartGateway(t, false)
	view := getSettingsView(t, g)
	as, _ := view["autostart"].(map[string]any)
	if as == nil || as["supported"] != false {
		t.Fatalf("无头模式应返回 unsupported: %v", view["autostart"])
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings/autostart", strings.NewReader(`{"on":true}`))
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("无头模式写入自启应 400，得到 %d", rec.Code)
	}
}

func TestAutoStartToggleDesktop(t *testing.T) {
	g := newAutoStartGateway(t, true)
	var enabled bool
	var gotCommand string
	g.autoStartGet = func() appstart.Status {
		return appstart.Status{Supported: true, Enabled: enabled, Command: gotCommand}
	}
	g.autoStartSet = func(on bool) error {
		enabled = on
		if on {
			gotCommand = `"C:\Apps\AetherCockpit.exe"`
		} else {
			gotCommand = ""
		}
		return nil
	}

	view := getSettingsView(t, g)
	as, _ := view["autostart"].(map[string]any)
	if as == nil || as["supported"] != true || as["enabled"] != false {
		t.Fatalf("初始自启状态不符: %v", view["autostart"])
	}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/autostart", strings.NewReader(`{"on":true}`))
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("开启失败: %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	as, _ = out["autostart"].(map[string]any)
	if as == nil || as["enabled"] != true || as["command"] == "" {
		t.Fatalf("开启后状态不符: %v", out)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/settings/autostart", strings.NewReader(`{"on":false}`))
	rec = httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if enabled {
		t.Fatal("关闭后仍处于启用状态")
	}
}
