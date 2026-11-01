package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/pool"
)

func newSettingsGateway(t *testing.T) *Gateway {
	t.Helper()
	dir := t.TempDir()
	return &Gateway{
		settingsPath: filepath.Join(dir, "settings.json"),
		models:       NewModelRouter(),
		pool:         pool.New(),
		callerKeys:   newCallerKeyStore(filepath.Join(dir, "caller-keys.json")),
	}
}

func TestUIPrefsPersistAndEcho(t *testing.T) {
	g := newSettingsGateway(t)
	body := `{"theme":"dark","lang":"ja","textSize":15,"quotaLeft":true,"privacy_mask":{"enabled":false}}`
	rec := httptest.NewRecorder()
	g.handleUpdateSettings(rec, httptest.NewRequest("POST", "/api/settings", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	var saved map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if saved["theme"] != "dark" || saved["lang"] != "ja" || saved["textSize"] != float64(15) {
		t.Fatalf("响应未回显 UI 偏好: %+v", saved)
	}

	rec = httptest.NewRecorder()
	g.handleSettings(rec, httptest.NewRequest("GET", "/api/settings", nil))
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["theme"] != "dark" || got["quotaLeft"] != true {
		t.Fatalf("GET 未回显: %+v", got)
	}
	if _, ok := got["settings"]; !ok {
		t.Fatal("GET 应保留 settings 别名")
	}

	// 落盘在 ui 下
	b, _ := os.ReadFile(g.settingsPath)
	if !strings.Contains(string(b), `"theme": "dark"`) {
		t.Fatalf("设置文件未持久化 UI 偏好: %s", string(b)[:200])
	}
}

func TestCORSAndAllowIPsHandlers(t *testing.T) {
	g := newSettingsGateway(t)

	rec := httptest.NewRecorder()
	g.handleCORSSettings(rec, httptest.NewRequest("POST", "/api/settings/cors", strings.NewReader(`{"origins":["https://ok.example","chrome-extension://abcdef"]}`)))
	if rec.Code != 200 {
		t.Fatalf("cors 保存: %d %s", rec.Code, rec.Body.String())
	}
	var view map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if list, _ := view["corsOrigins"].([]any); len(list) != 2 {
		t.Fatalf("corsOrigins = %v", view["corsOrigins"])
	}

	rec = httptest.NewRecorder()
	g.handleCORSSettings(rec, httptest.NewRequest("POST", "/api/settings/cors", strings.NewReader(`{"origins":["not a url"]}`)))
	if rec.Code != 400 {
		t.Fatalf("非法来源应 400，得到 %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	g.handleAllowIPsSettings(rec, httptest.NewRequest("POST", "/api/settings/allowips", strings.NewReader(`{"ips":["10.0.0.0/8","1.2.3.4"]}`)))
	if rec.Code != 200 {
		t.Fatalf("allowips 保存: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	g.handleAllowIPsSettings(rec, httptest.NewRequest("POST", "/api/settings/allowips", strings.NewReader(`{"ips":["999.1.1.1"]}`)))
	if rec.Code != 400 {
		t.Fatalf("非法 IP 应 400，得到 %d", rec.Code)
	}
}

func TestAllowIPsEnforced(t *testing.T) {
	g := newSettingsGateway(t)
	_, secret := g.callerKeys.add("K")
	g.settings.AllowIPs = []string{"10.0.0.0/8"}

	h := g.auth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("不在白名单应 403，得到 %d", rec.Code)
	}

	g.settings.AllowIPs = []string{"1.2.3.0/24"}
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != 200 {
		t.Fatalf("在白名单应 200，得到 %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCORSHeadersHonorOrigins(t *testing.T) {
	g := newSettingsGateway(t)
	g.settings.CORSOrigins = []string{"https://ok.example"}

	h := g.auth(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	req := httptest.NewRequest("OPTIONS", "/v1/chat/completions", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Origin", "https://ok.example")
	rec := httptest.NewRecorder()
	h(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://ok.example" {
		t.Fatalf("允许来源 ACAO = %q", got)
	}

	req = httptest.NewRequest("OPTIONS", "/v1/chat/completions", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Origin", "https://bad.example")
	rec = httptest.NewRecorder()
	h(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("未允许来源不应回 ACAO，得到 %q", got)
	}
}

func TestProviderSwitchFiltersRoutingAndModels(t *testing.T) {
	g := newSettingsGateway(t)
	g.models.Rebuild()
	g.models.SetEnabledFilter(g.providerIsEnabled)
	if got := g.models.Resolve("mock-native/echo-pro"); len(got) == 0 {
		t.Fatal("启用时应能解析到 mock-native")
	}
	rec := httptest.NewRecorder()
	g.handleProviderSwitch(rec, httptest.NewRequest("POST", "/api/provider/off", strings.NewReader(`{"id":"mock-native"}`)))
	if rec.Code != 200 {
		t.Fatalf("off: %d %s", rec.Code, rec.Body.String())
	}
	if got := g.models.Resolve("mock-native/echo-pro"); len(got) != 0 {
		t.Fatalf("停用后仍能解析: %+v", got)
	}
	rec = httptest.NewRecorder()
	g.handleModels(rec, httptest.NewRequest("GET", "/v1/models", nil))
	if strings.Contains(rec.Body.String(), "mock-native/echo-pro") {
		t.Fatal("/v1/models 仍列出停用渠道的模型")
	}
	// 恢复
	rec = httptest.NewRecorder()
	g.handleProviderSwitch(rec, httptest.NewRequest("POST", "/api/provider/on", strings.NewReader(`{"id":"mock-native"}`)))
	if len(g.models.Resolve("mock-native/echo-pro")) == 0 {
		t.Fatal("重新启用后未恢复")
	}
}

func TestUISettingWhitelistAndRefusal(t *testing.T) {
	g := newSettingsGateway(t)
	rec := httptest.NewRecorder()
	req0 := httptest.NewRequest("POST", "/api/settings/text-size", strings.NewReader(`{"size":16}`))
	req0.SetPathValue("key", "text-size")
	g.handleUISetting(rec, req0)
	if rec.Code != 200 {
		t.Fatalf("text-size: %d %s", rec.Code, rec.Body.String())
	}
	var view map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view["textSize"] != float64(16) {
		t.Fatalf("textSize = %v", view["textSize"])
	}
	// port 由专用的 /api/settings/port（可热切换）处理，泛化入口不应假装保存。
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/settings/port", strings.NewReader(`{"port":8080}`))
	req.SetPathValue("key", "port")
	g.handleUISetting(rec, req)
	if rec.Code != 404 {
		t.Fatalf("port 泛化入口应 404，得到 %d", rec.Code)
	}
	req = httptest.NewRequest("POST", "/api/settings/nonsense", strings.NewReader(`{}`))
	req.SetPathValue("key", "nonsense")
	rec = httptest.NewRecorder()
	g.handleUISetting(rec, req)
	if rec.Code != 404 {
		t.Fatalf("未知项应 404，得到 %d", rec.Code)
	}
}
