package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func portCall(t *testing.T, g *Gateway, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handlePortSetting(rec, httptest.NewRequest(http.MethodPost, "/api/settings/port", strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestPortSwitch(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:7860", SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	var got string
	g.SetPortSwitcher(func(addr string) (string, error) {
		got = addr
		return addr, nil
	})
	code, out := portCall(t, g, `{"port":7999}`)
	if code != http.StatusOK {
		t.Fatalf("code=%d out=%#v", code, out)
	}
	if got != "127.0.0.1:7999" || g.PublicAddr() != "127.0.0.1:7999" {
		t.Fatalf("switch got=%q public=%q", got, g.PublicAddr())
	}
	if out["port"].(float64) != 7999 || out["url"] != "http://127.0.0.1:7999" {
		t.Fatalf("out = %#v", out)
	}
	g.settingsMu.Lock()
	saved := g.settings.ListenAddr
	g.settingsMu.Unlock()
	if saved != "127.0.0.1:7999" {
		t.Fatalf("settings listen_addr = %q", saved)
	}

	// 只写端口时沿用当前主机；带主机时保留。
	code, _ = portCall(t, g, `{"addr":"0.0.0.0:8080"}`)
	if code != http.StatusOK || got != "0.0.0.0:8080" {
		t.Fatalf("addr code=%d got=%q", code, got)
	}
	code, _ = portCall(t, g, `{"port":"9001"}`)
	if code != http.StatusOK || got != "0.0.0.0:9001" {
		t.Fatalf("inherit host: code=%d got=%q", code, got)
	}
}

func TestPortSwitchValidation(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:7860", SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	if code, _ := portCall(t, g, `{"port":70000}`); code != http.StatusBadRequest {
		t.Fatalf("70000 应 400，得到 %d", code)
	}
	if code, _ := portCall(t, g, `{"port":"abc"}`); code != http.StatusBadRequest {
		t.Fatalf("abc 应 400，得到 %d", code)
	}
	if code, _ := portCall(t, g, `{}`); code != http.StatusBadRequest {
		t.Fatalf("缺 port 应 400，得到 %d", code)
	}
	// 没有切换回调的运行模式：明确拒绝。
	if code, out := portCall(t, g, `{"port":7999}`); code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "不支持") {
		t.Fatalf("无回调: code=%d out=%#v", code, out)
	}
}

func TestPortPersistsAndOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	g := New(Config{Addr: "127.0.0.1:7860", SettingsPath: path, UsagePath: "-"})
	g.SetPortSwitcher(func(addr string) (string, error) { return addr, nil })
	if code, _ := portCall(t, g, `{"port":7999}`); code != http.StatusOK {
		t.Fatal("切换失败")
	}
	g2 := New(Config{Addr: "127.0.0.1:7860", SettingsPath: path, UsagePath: "-"})
	if g2.ListenAddr() != "127.0.0.1:7999" {
		t.Fatalf("重启后 ListenAddr = %q", g2.ListenAddr())
	}
}

func TestPortIdempotentAndUnifiedSettings(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:7860", SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	calls := 0
	g.SetPortSwitcher(func(addr string) (string, error) { calls++; return addr, nil })

	// 保存整份 prefs 时回传当前端口：不应重新监听。
	if _, err := g.applyPort("7860"); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("同端口触发切换 %d 次", calls)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"port":7999}`))
	g.handleUpdateSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings 保存 = %d %s", rec.Code, rec.Body.String())
	}
	if calls != 1 || g.PublicAddr() != "127.0.0.1:7999" {
		t.Fatalf("calls=%d public=%q", calls, g.PublicAddr())
	}
	g.settingsMu.Lock()
	_, stored := g.settings.UI["port"]
	g.settingsMu.Unlock()
	if stored {
		t.Fatal("port 不应进入 UI 透传偏好")
	}
}
