package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func proxyCall(t *testing.T, g *Gateway, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/settings/proxy", strings.NewReader(body))
	rec := httptest.NewRecorder()
	g.handleProxySetting(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestProxyRuntimeSwitch(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	env := provider.NewAccountEnv("proxy-acc", "mock-native")
	env.NetworkPolicy = provider.NetPolicyProxyFirst
	g.pool.Add(env)

	if code, _ := proxyCall(t, g, `{}`); code != http.StatusBadRequest {
		t.Fatalf("缺 proxy 应 400，得到 %d", code)
	}
	if code, _ := proxyCall(t, g, `{"proxy":"ftp://127.0.0.1:21"}`); code != http.StatusBadRequest {
		t.Fatalf("非法协议应 400，得到 %d", code)
	}
	if g.tr.GlobalProxyValue() != "" {
		t.Fatal("非法输入不应改变当前代理")
	}

	code, out := proxyCall(t, g, `{"proxy":"socks5://127.0.0.1:1080"}`)
	if code != http.StatusOK || out["proxy"] != "socks5://127.0.0.1:1080" {
		t.Fatalf("code=%d out=%#v", code, out)
	}
	if g.tr.GlobalProxyValue() != "socks5://127.0.0.1:1080" {
		t.Fatal("工厂未切换")
	}
	g.settingsMu.Lock()
	saved := g.settings.GlobalProxy
	g.settingsMu.Unlock()
	if saved != "socks5://127.0.0.1:1080" {
		t.Fatalf("settings.GlobalProxy = %q", saved)
	}
	// 账号客户端被热替换。
	tr := env.Client().Transport.(*http.Transport)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if u, _ := tr.Proxy(req); u == nil || u.Host != "127.0.0.1:1080" {
		t.Fatalf("账号客户端未应用代理: %v", u)
	}

	if code, _ := proxyCall(t, g, `{"proxy":""}`); code != http.StatusOK {
		t.Fatalf("关闭代理失败: %d", code)
	}
	if g.tr.GlobalProxyValue() != "" {
		t.Fatal("关闭后仍有代理")
	}
}

func TestProxyCredentialsMasked(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	code, out := proxyCall(t, g, `{"proxy":"http://alice:s3cret@127.0.0.1:8080"}`)
	if code != http.StatusOK {
		t.Fatalf("code=%d out=%#v", code, out)
	}
	masked, _ := out["proxy"].(string)
	if masked != "http://alice:******@127.0.0.1:8080" {
		t.Fatalf("masked = %q", masked)
	}
	if strings.Contains(masked, "s3cret") {
		t.Fatal("响应泄漏了代理密码")
	}

	// 原样回传打码值：沿用旧密码，不把 ****** 当成真密码。
	code, out = proxyCall(t, g, `{"proxy":`+strconvQuote(masked)+`}`)
	if code != http.StatusOK {
		t.Fatalf("masked resubmit code=%d out=%#v", code, out)
	}
	g.settingsMu.Lock()
	saved := g.settings.GlobalProxy
	g.settingsMu.Unlock()
	u, err := url.Parse(saved)
	if err != nil {
		t.Fatal(err)
	}
	pass, _ := u.User.Password()
	if pass != "s3cret" {
		t.Fatalf("密码被覆盖: %q", saved)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestProxyPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	g := New(Config{SettingsPath: path, UsagePath: "-"})
	if code, _ := proxyCall(t, g, `{"proxy":"socks5://127.0.0.1:1081"}`); code != http.StatusOK {
		t.Fatal("设置失败")
	}
	g2 := New(Config{SettingsPath: path, UsagePath: "-"})
	if got := g2.tr.GlobalProxyValue(); got != "socks5://127.0.0.1:1081" {
		t.Fatalf("重启后代理 = %q", got)
	}
}
