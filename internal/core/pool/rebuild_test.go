package pool

import (
	"net/http"
	"testing"

	"aether-cockpit/internal/core/transport"
	"aether-cockpit/internal/provider"
)

func TestRebuildClientsHotSwap(t *testing.T) {
	m := New()
	proxied := provider.NewAccountEnv("acc-proxy", "p1")
	proxied.NetworkPolicy = provider.NetPolicyProxyFirst
	proxied.SetHTTPClient(http.DefaultClient)
	m.Add(proxied)

	direct := provider.NewAccountEnv("acc-direct", "p2")
	direct.NetworkPolicy = provider.NetPolicyDirectForce
	direct.SetHTTPClient(http.DefaultClient)
	m.Add(direct)

	if pointed := proxied.Client(); pointed != http.DefaultClient {
		t.Fatal("初始客户端应保持")
	}

	f := transport.NewFactory("socks5://127.0.0.1:1080")
	n, err := m.RebuildClients(f)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if proxied.Client() == http.DefaultClient {
		t.Fatal("代理账号的客户端应被替换")
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	tr := proxied.Client().Transport.(*http.Transport)
	if u, _ := tr.Proxy(req); u == nil || u.Host != "127.0.0.1:1080" {
		t.Fatalf("proxy-first 未应用新代理: %v", u)
	}
	tr2 := direct.Client().Transport.(*http.Transport)
	if tr2.Proxy != nil {
		if u, _ := tr2.Proxy(req); u != nil {
			t.Fatalf("direct_force 不应走代理: %v", u)
		}
	}

	// 失败时保持旧客户端：用非法地址让工厂建客户端失败。
	f2 := transport.NewFactory("")
	if err := f2.SetGlobalProxy(""); err != nil {
		t.Fatal(err)
	}
	before := proxied.Client()
	if _, err := m.RebuildClients(nil); err == nil {
		t.Fatal("空工厂应报错")
	}
	if proxied.Client() != before {
		t.Fatal("失败时不应替换客户端")
	}
}
