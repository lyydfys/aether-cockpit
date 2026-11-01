package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/tools"
)

func TestImageTestHandler(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "img-model" {
			t.Errorf("model = %v", body["model"])
		}
		_, _ = w.Write([]byte(`{"data":[{"url":"https://img.example/1.png"}]}`))
	}))
	defer up.Close()

	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.settingsMu.Lock()
	g.settings.Tools = &ToolsConfig{Image: &tools.ImageConfig{BaseURL: up.URL, APIKey: "ik", Model: "img-model"}}
	g.settingsMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/tools/image-test", g.handleImageTest)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/tools/image-test", strings.NewReader("{}")))
	if rec.Code != 200 {
		t.Fatalf("test = %d %s", rec.Code, rec.Body.String())
	}
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["ok"] != true || !strings.Contains(out["summary"].(string), "img.example") {
		t.Fatalf("out = %v", out)
	}

	// 未配置：明确 400。
	g2 := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	mux2 := http.NewServeMux()
	mux2.HandleFunc("POST /api/tools/image-test", g2.handleImageTest)
	rec = httptest.NewRecorder()
	mux2.ServeHTTP(rec, httptest.NewRequest("POST", "/api/tools/image-test", strings.NewReader("{}")))
	if rec.Code != 400 {
		t.Fatalf("unconfigured = %d", rec.Code)
	}
}

func TestApplyToolsImageKeepsKey(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.settingsMu.Lock()
	g.settings.Tools = &ToolsConfig{Image: &tools.ImageConfig{BaseURL: "https://a", APIKey: "ik"}}
	g.applyToolsConfig(&ToolsConfig{Image: &tools.ImageConfig{BaseURL: "https://b", APIKey: webdavPasswordMask}})
	g.settingsMu.Unlock()
	if g.settings.Tools.Image.APIKey != "ik" {
		t.Fatalf("占位符应沿用旧 Key: %+v", g.settings.Tools.Image)
	}

	// 没有旧配置时，占位符不能被当成 Key 存下。
	g2 := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g2.settingsMu.Lock()
	g2.applyToolsConfig(&ToolsConfig{Image: &tools.ImageConfig{BaseURL: "https://a", APIKey: webdavPasswordMask}})
	g2.settingsMu.Unlock()
	if g2.settings.Tools.Image.APIKey != "" {
		t.Fatalf("占位符被存成 Key: %+v", g2.settings.Tools.Image)
	}
}
