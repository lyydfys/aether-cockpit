package transport

import (
	"net/http"
	"testing"

	"aether-cockpit/internal/provider"
)

func proxyOf(t *testing.T, f *Factory, policy provider.NetworkPolicy) string {
	t.Helper()
	c, err := f.NewClient(policy, "", "")
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T", c.Transport)
	}
	if tr.Proxy == nil {
		return ""
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/", nil)
	u, err := tr.Proxy(req)
	if err != nil || u == nil {
		return ""
	}
	return u.String()
}

func TestSetGlobalProxy(t *testing.T) {
	f := NewFactory("")
	if got := proxyOf(t, f, provider.NetPolicyProxyFirst); got != "" {
		t.Fatalf("初始不应有代理: %q", got)
	}
	if err := f.SetGlobalProxy("socks5://127.0.0.1:1080"); err != nil {
		t.Fatal(err)
	}
	if got := proxyOf(t, f, provider.NetPolicyProxyFirst); got != "socks5://127.0.0.1:1080" {
		t.Fatalf("proxy = %q", got)
	}
	// direct_force 永远直连，不受全局代理影响。
	if got := proxyOf(t, f, provider.NetPolicyDirectForce); got != "" {
		t.Fatalf("direct_force 不应走代理: %q", got)
	}
	// 运行中清空。
	if err := f.SetGlobalProxy(""); err != nil {
		t.Fatal(err)
	}
	if got := proxyOf(t, f, provider.NetPolicyProxyFirst); got != "" {
		t.Fatalf("清空后仍有代理: %q", got)
	}
}

func TestSetGlobalProxyRejectsBadInput(t *testing.T) {
	f := NewFactory("")
	for _, bad := range []string{"ftp://127.0.0.1:21", "socks5://", "http://[::1"} {
		if err := f.SetGlobalProxy(bad); err == nil {
			t.Fatalf("%q 应被拒绝", bad)
		}
	}
	if f.GlobalProxyValue() != "" {
		t.Fatalf("失败后不应改变当前值: %q", f.GlobalProxyValue())
	}
}

func TestDirectOverride(t *testing.T) {
	f := NewFactory("socks5://127.0.0.1:1080")
	c, err := f.NewClient(provider.NetPolicyProxyFirst, "direct", "")
	if err != nil {
		t.Fatal(err)
	}
	tr := c.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatal("direct 覆盖应强制直连")
	}
}
