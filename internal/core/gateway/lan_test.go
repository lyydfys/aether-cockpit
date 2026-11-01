package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newLANGateway(t *testing.T) *Gateway {
	t.Helper()
	dir := t.TempDir()
	g := &Gateway{
		settingsPath: filepath.Join(dir, "settings.json"),
		callerKeys:   newCallerKeyStore(filepath.Join(dir, "caller-keys.json")),
	}
	g.lan.cfgAddr = "127.0.0.1:0"
	return g
}

func TestLANRequiresRemoteCredential(t *testing.T) {
	g := newLANGateway(t)
	rec := httptest.NewRecorder()
	g.handleLANSettings(rec, httptest.NewRequest("POST", "/api/settings/lan", strings.NewReader(`{"on":true}`)))
	if rec.Code != 400 {
		t.Fatalf("没有密钥时打开局域网应 400，得到 %d: %s", rec.Code, rec.Body.String())
	}
	if g.LANEnabled() {
		t.Fatal("失败后不应处于开启状态")
	}
}

func TestLANStartStopAndSettingsView(t *testing.T) {
	g := newLANGateway(t)
	g.callerKeys.add("T")
	rec := httptest.NewRecorder()
	g.handleLANSettings(rec, httptest.NewRequest("POST", "/api/settings/lan", strings.NewReader(`{"on":true}`)))
	if rec.Code != 200 {
		t.Fatalf("on: %d %s", rec.Code, rec.Body.String())
	}
	var view map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view["lan"] != true {
		t.Fatalf("view.lan = %v", view["lan"])
	}
	urls, _ := view["lanURLs"].([]any)
	if len(urls) == 0 || !strings.HasPrefix(urls[0].(string), "http://127.0.0.1:") {
		t.Fatalf("lanURLs = %v", view["lanURLs"])
	}
	g.settingsMu.Lock()
	persisted := g.settings.LAN
	g.settingsMu.Unlock()
	if !persisted {
		t.Fatal("设置未持久化 lan=true")
	}

	// GET /api/settings 也要带 lan / lanURLs
	rec = httptest.NewRecorder()
	g.handleSettings(rec, httptest.NewRequest("GET", "/api/settings", nil))
	var got struct {
		Settings map[string]any `json:"settings"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Settings["lan"] != true {
		t.Fatalf("GET settings.lan = %v", got.Settings["lan"])
	}

	// off
	rec = httptest.NewRecorder()
	g.handleLANSettings(rec, httptest.NewRequest("POST", "/api/settings/lan", strings.NewReader(`{"on":false}`)))
	if rec.Code != 200 || g.LANEnabled() {
		t.Fatalf("off: %d enabled=%v", rec.Code, g.LANEnabled())
	}
}

func TestLANURLsForLoopback(t *testing.T) {
	urls := lanURLsFor("127.0.0.1:1234")
	if len(urls) != 1 || urls[0] != "http://127.0.0.1:1234" {
		t.Fatalf("urls = %v", urls)
	}
}
