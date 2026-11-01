package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func privacyModelGet(t *testing.T, g *Gateway, body string) map[string]any {
	t.Helper()
	method := http.MethodGet
	var r *http.Request
	if body != "" {
		method = http.MethodPost
		r = httptest.NewRequest(method, "/api/privacy-model", strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, "/api/privacy-model", nil)
	}
	rec := httptest.NewRecorder()
	g.handlePrivacyModel(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("privacy-model = %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("非 JSON: %v", err)
	}
	return out
}

func TestPrivacyModelBridge(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	g := New(Config{SettingsPath: settings, UsagePath: "-"})

	// 没配置边车：如实说不支持。
	out := privacyModelGet(t, g, "")
	if out["supported"] != false || out["installed"] != false || out["on"] != false {
		t.Fatalf("初始状态 = %v", out)
	}

	// 配一个必然存在的可执行文件（测试二进制自身），并开启遮蔽。
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body := `{"privacy_mask":{"enabled":true,"rules":["keys"],"sidecar":{"command":` +
		jsonString(exe) + `,"args":["-test.run=^$"]}}}`
	rec := httptest.NewRecorder()
	g.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("保存隐私设置失败: %d %s", rec.Code, rec.Body.String())
	}

	out = privacyModelGet(t, g, "")
	if out["supported"] != true || out["installed"] != true || out["on"] != true || out["running"] != true {
		// 边车是 fork 自身的测试二进制：内存吃紧时会 ENOMEM（"paging file is
		// too small"），那是环境上限，按 NOT-RUN 处理而不是报缺陷。
		if msg, _ := out["failed"].(string); resourceStarved(msg) {
			t.Skipf("系统资源不足，边车子进程起不来：%s", msg)
		}
		t.Fatalf("配置后状态 = %v", out)
	}
	if out["size"].(float64) <= 0 {
		t.Fatalf("应报告边车文件大小: %v", out["size"])
	}

	// off：停止边车，但遮蔽与命令保留。
	out = privacyModelGet(t, g, `{"do":"off"}`)
	if out["on"] != false || out["running"] != false {
		t.Fatalf("off 后 = %v", out)
	}
	if !g.settingsView()["redact"].(bool) && !g.settings.Privacy.Enabled {
		t.Fatal("off 不应关掉遮蔽总开关")
	}
	// on：重新启动。
	out = privacyModelGet(t, g, `{"do":"on"}`)
	if out["on"] != true || out["running"] != true {
		t.Fatalf("on 后 = %v", out)
	}

	// remove：清掉配置。
	out = privacyModelGet(t, g, `{"do":"remove"}`)
	if out["supported"] != false {
		t.Fatalf("remove 后 = %v", out)
	}
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"sidecar"`) {
		t.Fatalf("remove 后设置里仍有 sidecar: %s", raw)
	}

	// 未知动作 400。
	rec = httptest.NewRecorder()
	g.handlePrivacyModel(rec, httptest.NewRequest(http.MethodPost, "/api/privacy-model", strings.NewReader(`{"do":"download"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知动作 = %d", rec.Code)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
