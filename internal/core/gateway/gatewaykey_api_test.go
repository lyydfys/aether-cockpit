package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// 网关密钥应能一键生成：服务端用 crypto/rand 产出 sk-aether-<32hex>，
// 立即生效并持久化，明文只在这一次响应里返回。
//
// 参考移动端做法（caller-keys 的 add-key：服务端生成、只回显一次）。
func TestGatewayKeyGenerate(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	rec := httptest.NewRecorder()
	g.handleGatewayKeyGenerate(rec, httptest.NewRequest(http.MethodPost, "/api/settings/gateway-key", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("生成失败：%d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"api_key":"sk-aether-`) {
		t.Fatalf("应返回 sk-aether- 前缀的明文：%s", body)
	}

	key := g.GatewayKey()
	if !strings.HasPrefix(key, "sk-aether-") {
		t.Fatalf("密钥前缀不对：%q", key)
	}
	// sk-aether- (10) + 32 位十六进制
	if len(key) != len("sk-aether-")+32 {
		t.Fatalf("密钥长度 = %d，want %d（%q）", len(key), len("sk-aether-")+32, key)
	}
	for _, r := range strings.TrimPrefix(key, "sk-aether-") {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Fatalf("密钥主体应是小写十六进制，得到 %q", key)
		}
	}
	// 明文只给一次：回显必须是打码值。
	view := g.settingsView()
	if masked, _ := view["api_key"].(string); masked == key {
		t.Fatal("GET 不该回显明文")
	}
}

// 每次生成都不一样（否则等于所有实例共用一个密钥）。
func TestGatewayKeyGenerateIsRandom(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		k := newGatewayKey()
		if seen[k] {
			t.Fatalf("生成了重复密钥：%q", k)
		}
		seen[k] = true
	}
}

// 生成后要能持久化：重新 loadSettings 仍拿得到（重启不丢）。
func TestGatewayKeyGeneratePersists(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	}
	g := New(cfg)
	rec := httptest.NewRecorder()
	g.handleGatewayKeyGenerate(rec, httptest.NewRequest(http.MethodPost, "/api/settings/gateway-key", nil))
	key := g.GatewayKey()

	// 新实例从同一份 settings.json 启动，应恢复同一个密钥。
	g2 := New(cfg)
	if got := g2.GatewayKey(); got != key {
		t.Fatalf("重启后密钥 = %q，want %q", got, key)
	}
}

// 手填太短的密钥要被拒（弱口令等于没设），并提示用「生成」。
func TestShortManualGatewayKeyRejected(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	for _, weak := range []string{"a", "short", "sk-1234567890"} {
		rec := httptest.NewRecorder()
		g.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings",
			strings.NewReader(`{"api_key":"`+weak+`"}`)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%q 应被拒，得到 %d %s", weak, rec.Code, rec.Body.String())
		}
		if g.GatewayKey() != "" {
			t.Fatalf("%q 被拒后不该生效", weak)
		}
	}
	// 够长的可以用。
	rec := httptest.NewRecorder()
	g.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings",
		strings.NewReader(`{"api_key":"my-own-long-secret-2026"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("16 字符以上应可用：%d %s", rec.Code, rec.Body.String())
	}
	if g.GatewayKey() != "my-own-long-secret-2026" {
		t.Fatalf("手填未生效：%q", g.GatewayKey())
	}
}

// 空串仍然允许（= 关闭鉴权），不能被长度校验误伤。
func TestEmptyGatewayKeyStillAllowed(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	g.SetGatewayKey("sk-aether-existing-key-1234")
	rec := httptest.NewRecorder()
	g.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings",
		strings.NewReader(`{"api_key":""}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("清空应允许：%d %s", rec.Code, rec.Body.String())
	}
	if g.GatewayKey() != "" {
		t.Fatalf("应为空，得到 %q", g.GatewayKey())
	}
}
