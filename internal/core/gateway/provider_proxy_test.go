package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/transport"
	"aether-cockpit/internal/provider"
)

func TestProviderAndAccountProxyPrecedence(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	env := provider.NewAccountEnv("acc-1", "workbuddy-intl")
	env.Label = "alice"
	env.NetworkPolicy = provider.NetPolicyProxyFirst
	g.pool.Add(env)

	// 全局代理 → 渠道代理 → 账号代理，逐级覆盖。
	if code, _ := proxyCall(t, g, `{"proxy":"socks5://127.0.0.1:1080"}`); code != 200 {
		t.Fatal("设置全局代理失败")
	}
	if got := env.Proxy(); got != "" {
		t.Fatalf("初始覆盖 = %q", got)
	}

	prov := "http://127.0.0.1:8080"
	if err := g.setProviderProxy("workbuddy-intl", &prov, nil); err != nil {
		t.Fatal(err)
	}
	if got := env.Proxy(); got != prov {
		t.Fatalf("渠道代理 = %q", got)
	}
	tr := transport.UnwrapTransport(env.Client().Transport)
	if tr == nil {
		t.Fatalf("transport = %T", env.Client().Transport)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if u, _ := tr.Proxy(req); u == nil || u.Host != "127.0.0.1:8080" {
		t.Fatalf("渠道代理未生效: %v", u)
	}

	direct := "direct"
	if err := g.setProviderProxy("workbuddy-intl", nil, map[string]string{"alice": direct}); err != nil {
		t.Fatal(err)
	}
	if got := env.Proxy(); got != "direct" {
		t.Fatalf("账号代理 = %q", got)
	}
	tr = transport.UnwrapTransport(env.Client().Transport)
	if tr.Proxy != nil {
		t.Fatalf("direct 覆盖后不应有代理")
	}

	// 清掉账号覆盖回落到渠道代理。
	if err := g.setProviderProxy("workbuddy-intl", nil, map[string]string{"alice": ""}); err != nil {
		t.Fatal(err)
	}
	if got := env.Proxy(); got != prov {
		t.Fatalf("回落 = %q", got)
	}

	// 清掉渠道代理回落全局。
	empty := ""
	if err := g.setProviderProxy("workbuddy-intl", &empty, nil); err != nil {
		t.Fatal(err)
	}
	if got := env.Proxy(); got != "" {
		t.Fatalf("渠道清空后覆盖 = %q", got)
	}
	tr = transport.UnwrapTransport(env.Client().Transport)
	if u, _ := tr.Proxy(req); u == nil || u.Host != "127.0.0.1:1080" {
		t.Fatalf("未回落到全局代理: %v", u)
	}
}

func TestDirectForceWithExplicitProxy(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	env := provider.NewAccountEnv("acc-2", "workbuddy-cn")
	env.Label = "bob"
	env.NetworkPolicy = provider.NetPolicyDirectForce
	g.pool.Add(env)
	_, _ = proxyCall(t, g, `{"proxy":"socks5://127.0.0.1:1080"}`)

	// direct_force 默认忽略全局代理。
	tr := transport.UnwrapTransport(env.Client().Transport)
	if tr == nil {
		t.Fatalf("transport = %T", env.Client().Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("direct_force 不应走全局代理")
	}
	// 但显式渠道代理可以覆盖。
	prov := "http://127.0.0.1:8081"
	if err := g.setProviderProxy("workbuddy-cn", &prov, nil); err != nil {
		t.Fatal(err)
	}
	tr = transport.UnwrapTransport(env.Client().Transport)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if u, _ := tr.Proxy(req); u == nil || u.Host != "127.0.0.1:8081" {
		t.Fatalf("显式代理未覆盖 direct_force: %v", u)
	}
}

func TestProviderProxyViewMasked(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	prov := "http://bob:secret@127.0.0.1:8080"
	if err := g.setProviderProxy("openaicompat", &prov, map[string]string{"carol": "socks5://127.0.0.1:1081"}); err != nil {
		t.Fatal(err)
	}
	proxy, accounts := g.providerProxyView("openaicompat")
	if proxy != "http://bob:******@127.0.0.1:8080" {
		t.Fatalf("proxy view = %q", proxy)
	}
	if accounts["carol"] != "socks5://127.0.0.1:1081" {
		t.Fatalf("account view = %#v", accounts)
	}
}

func TestProviderSaveProxyPayload(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	body := `{"id":"mock-native","name":"alice","key":"tk","proxy":"http://127.0.0.1:8082","accountProxies":{"alice":"direct"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/provider/save", strings.NewReader(body))
	rec := httptest.NewRecorder()
	g.handleProviderSave(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	envs := g.pool.ListByProvider("mock-native")
	if len(envs) != 1 {
		t.Fatalf("accounts = %d", len(envs))
	}
	if got := envs[0].Proxy(); got != "direct" {
		t.Fatalf("账号代理应覆盖渠道: %q", got)
	}
	// providers 响应里带打码后的 proxy/accountProxies。
	var out struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	for _, p := range out.Providers {
		if p["id"] == "mock-native" {
			row = p
		}
	}
	if row == nil || row["proxy"] != "http://127.0.0.1:8082" {
		t.Fatalf("provider row = %#v", row)
	}
	if ap, _ := row["accountProxies"].(map[string]any); ap["alice"] != "direct" {
		t.Fatalf("accountProxies = %#v", row["accountProxies"])
	}
}
