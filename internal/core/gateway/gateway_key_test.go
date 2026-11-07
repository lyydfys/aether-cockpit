package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// 设置页写入的密钥必须真的生效。
//
// 历史 bug：`api_key` 没有对应的内核分支，被当成 UI 偏好塞进 settings.ui，
// 而鉴权读的是 cfg.APIKey → 用户填了密钥仍然"没密钥"，局域网也因此一直被拒。
func TestSettingsAPIKeyTakesEffect(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	if g.GatewayKey() != "" {
		t.Fatalf("初始不该有密钥，得到 %q", g.GatewayKey())
	}

	body := `{"api_key":"sk-aether-test-1234"}`
	rec := httptest.NewRecorder()
	g.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("保存失败：%d %s", rec.Code, rec.Body.String())
	}
	if got := g.GatewayKey(); got != "sk-aether-test-1234" {
		t.Fatalf("密钥应立即生效，得到 %q", got)
	}

	// 回显必须是打码值，不能把明文吐回给任何能打开管理页的人。
	view := g.settingsView()
	if masked, _ := view["api_key"].(string); masked == "sk-aether-test-1234" {
		t.Fatalf("不该明文回显密钥")
	} else if !strings.Contains(masked, "1234") {
		t.Fatalf("打码值应保留尾四位，得到 %q", masked)
	}
	if set, _ := view["api_key_set"].(bool); !set {
		t.Fatalf("api_key_set 应为 true")
	}
	if ui, ok := g.settings.UI["api_key"]; ok {
		t.Fatalf("密钥不该落进 settings.ui（会变成不生效的副本）：%v", ui)
	}
}

// 有密钥之后，局域网开关必须被允许（它此前一直被"没密钥"挡住）。
func TestLANAllowedAfterSettingsKey(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	// 先设密钥
	rec := httptest.NewRecorder()
	g.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings",
		strings.NewReader(`{"api_key":"sk-aether-lan-test"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("设置密钥失败：%d %s", rec.Code, rec.Body.String())
	}
	// 再开局域网：应通过"密钥存在"这道门槛（后面会去绑端口，测里不校验结果）
	rec2 := httptest.NewRecorder()
	g.handleLANSettings(rec2, httptest.NewRequest(http.MethodPost, "/api/settings/lan",
		strings.NewReader(`{"on":true}`)))
	if rec2.Code == http.StatusBadRequest && strings.Contains(rec2.Body.String(), "密钥") {
		t.Fatalf("有密钥后不该再被密钥拦下：%d %s", rec2.Code, rec2.Body.String())
	}
}

// 空密钥 = 关闭鉴权（本机免密钥的合理默认）。
func TestSettingsAPIKeyCanBeCleared(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	g.SetGatewayKey("sk-aether-old")
	rec := httptest.NewRecorder()
	g.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings",
		strings.NewReader(`{"api_key":""}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("清空失败：%d %s", rec.Code, rec.Body.String())
	}
	if got := g.GatewayKey(); got != "" {
		t.Fatalf("清空后应为空，得到 %q", got)
	}
}

// maskGatewayKey 的形态：短密钥全打码，长密钥保留首三尾四。
func TestMaskGatewayKey(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"short":              "*****",
		"sk-aether-abcd1234": "sk-………1234",
	}
	for in, want := range cases {
		if got := maskGatewayKey(in); got != want {
			t.Errorf("maskGatewayKey(%q) = %q，want %q", in, got, want)
		}
	}
}
