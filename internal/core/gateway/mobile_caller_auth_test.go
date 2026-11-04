package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func mobileCallerReq(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// TestMobileCallerAllowedAcceptsStoreKeys 覆盖真机验证时发现的一个真实缺口：
//
// 移动模式起初只认运行时生成的那一把 `cfg.CallerKey`，**完全忽略** `/api/caller-keys`
// 里创建并启用的密钥。表现是：用户在手机上点「新建密钥」，页面显示"启用 · aek_…"，
// 但拿它去调 `/v1/models` 仍然 `caller_auth_required`——创建了却用不了。
//
// 期望：运行时 key 与已启用的调用方密钥**都**能通过；两者都不匹配时失败（fail-closed）。
func TestMobileCallerAllowedAcceptsStoreKeys(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		SettingsPath:     filepath.Join(dir, "settings.json"),
		CallerKey:        "runtime-caller-key",
		AdminSessionKey:  "admin-session-key",
	})
	g.callerKeys = newCallerKeyStore(filepath.Join(dir, "caller-keys.json"))
	_, secret := g.callerKeys.add("phone-local")

	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"运行时 key", "runtime-caller-key", true},
		{"管理面创建的 key", secret, true},
		{"错误 key", "nope", false},
		{"空 key", "", false},
	}
	for _, c := range cases {
		if got := g.mobileCallerAllowed(mobileCallerReq(c.token)); got != c.want {
			t.Fatalf("%s: mobileCallerAllowed = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestMobileCallerAllowedWorksWithoutRuntimeKey：没有配置运行时 key、但存在已启用的
// 调用方密钥时也必须能通过；停用后必须立刻失效。
func TestMobileCallerAllowedWorksWithoutRuntimeKey(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AdminSessionKey:  "admin-session-key",
	})
	g.callerKeys = newCallerKeyStore(filepath.Join(dir, "caller-keys.json"))
	key, secret := g.callerKeys.add("phone-local")

	if !g.mobileCallerAllowed(mobileCallerReq(secret)) {
		t.Fatal("没有任何运行时 key 时，已启用的调用方密钥必须能通过")
	}

	if !g.callerKeys.update(key.ID, func(k *callerKey) bool { k.Off = true; return true }) {
		t.Fatal("停用失败，测试前提不成立")
	}
	if g.mobileCallerAllowed(mobileCallerReq(secret)) {
		t.Fatal("停用后的密钥不得再通过")
	}
}

// TestMobileCallerAllowedFailsClosedOnEmptyConfig 两者都没有时一律拒绝——
// 不能因为"没配置"就放行回环请求。
func TestMobileCallerAllowedFailsClosedOnEmptyConfig(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AdminSessionKey:  "admin-session-key",
	})
	g.callerKeys = newCallerKeyStore(filepath.Join(dir, "caller-keys.json"))

	for _, token := range []string{"", "anything"} {
		if g.mobileCallerAllowed(mobileCallerReq(token)) {
			t.Fatalf("token=%q 时不应放行", token)
		}
	}
}
