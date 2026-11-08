package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// 设置页的「局域网访问」开关走 /api/settings（不是 /api/settings/lan），
// 这条路径必须同样受"先有可用凭证"约束——否则能绕过守卫开出一个全 401 的端口。
func TestSettingsLANBranchRespectsCredentialGuard(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	// 无密钥：应被拒，且不能进入开启状态。
	rec := httptest.NewRecorder()
	g.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings",
		strings.NewReader(`{"lan":true}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("无密钥时应拒绝，得到 %d %s", rec.Code, rec.Body.String())
	}
	if g.LANEnabled() {
		t.Fatal("被拒后不该处于开启状态")
	}

	// 设密钥后再开：通过。
	rec2 := httptest.NewRecorder()
	g.handleUpdateSettings(rec2, httptest.NewRequest(http.MethodPost, "/api/settings",
		strings.NewReader(`{"api_key":"sk-aether-lan-branch"}`)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("设置密钥失败：%d %s", rec2.Code, rec2.Body.String())
	}
	rec3 := httptest.NewRecorder()
	g.lan.cfgAddr = "127.0.0.1:0"
	g.handleUpdateSettings(rec3, httptest.NewRequest(http.MethodPost, "/api/settings",
		strings.NewReader(`{"lan":true}`)))
	if rec3.Code != http.StatusOK {
		t.Fatalf("有密钥后应能开启，得到 %d %s", rec3.Code, rec3.Body.String())
	}
	if !g.LANEnabled() {
		t.Fatal("应处于开启状态")
	}
	// 收尾：关掉，避免测试进程占端口。
	_ = g.SetLAN(false)
}

// 关闭局域网永远允许（包括没有密钥时），否则用户会被"开不了也关不掉"卡住。
func TestSettingsLANCanAlwaysDisable(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	rec := httptest.NewRecorder()
	g.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings",
		strings.NewReader(`{"lan":false}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("关闭应总是允许，得到 %d %s", rec.Code, rec.Body.String())
	}
	if g.LANEnabled() {
		t.Fatal("应处于关闭状态")
	}
}

// SetLAN 是三条入口（settings / settings/lan / RestoreLAN）的共同底层，
// 守卫放在这里才能保证口径一致。
func TestSetLANGuardIsSharedByAllEntrypoints(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	g.lan.cfgAddr = "127.0.0.1:0"
	if err := g.SetLAN(true); err == nil {
		t.Fatal("没有凭证时 SetLAN 应返回错误")
	}
	if g.LANEnabled() {
		t.Fatal("失败后不该开启")
	}
}
