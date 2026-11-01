package gateway

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestProxySecretsMaskedInSettings(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.settingsMu.Lock()
	g.settings.GlobalProxy = "http://alice:s3cret@127.0.0.1:8080"
	g.settings.ProviderProxies = map[string]string{"p": "http://bob:hunter2@127.0.0.1:8081", "q": "direct"}
	g.settings.AccountProxies = map[string]string{"p\x00u": "socks5://carol:pass3@127.0.0.1:1080"}
	g.settingsMu.Unlock()

	view := g.settingsView()
	b, _ := json.Marshal(view)
	text := string(b)
	for _, secret := range []string{"s3cret", "hunter2", "pass3"} {
		if strings.Contains(text, secret) {
			t.Fatalf("settingsView 泄漏了代理密码 %q: %s", secret, text)
		}
	}
	if view["proxy"] != "http://alice:******@127.0.0.1:8080" {
		t.Fatalf("proxy = %v", view["proxy"])
	}
	pp, _ := view["provider_proxies"].(map[string]any)
	if pp["p"] != "http://bob:******@127.0.0.1:8081" || pp["q"] != "direct" {
		t.Fatalf("provider_proxies = %#v", pp)
	}
	ap, _ := view["account_proxies"].(map[string]any)
	if ap["p\x00u"] != "socks5://carol:******@127.0.0.1:1080" {
		t.Fatalf("account_proxies = %#v", ap)
	}
}

func TestBackupExcludesProxySecrets(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.settingsMu.Lock()
	g.settings.GlobalProxy = "http://alice:s3cret@127.0.0.1:8080"
	g.settings.ProviderProxies = map[string]string{"p": "http://bob:hunter2@127.0.0.1:8081"}
	g.settings.AccountProxies = map[string]string{"p\x00u": "socks5://carol:pass3@127.0.0.1:1080"}
	g.settingsMu.Unlock()

	bundle := g.buildBackupBundle()
	if bundle.Settings == nil {
		t.Fatal("缺少 settings")
	}
	if bundle.Settings.GlobalProxy != "" || len(bundle.Settings.ProviderProxies) != 0 || len(bundle.Settings.AccountProxies) != 0 {
		t.Fatalf("备份包含代理配置: %#v", bundle.Settings)
	}
	b, _ := json.Marshal(bundle)
	for _, secret := range []string{"s3cret", "hunter2", "pass3"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("备份泄漏代理密码 %q", secret)
		}
	}
}
