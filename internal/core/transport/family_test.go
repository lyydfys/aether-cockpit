package transport

import (
	"net/http"
	"testing"
)

func mustRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// IPv4 必须排在 IPv6 前面：国内不少网络 IPv6 半通（能解析、连不上），
// 按 DNS 原序拨号会有一半概率撞上去。
func TestOrderByFamilyPrefersV4(t *testing.T) {
	in := []string{"2606:4700::6810:1234", "104.26.12.1", "2001:db8::1", "172.67.210.105"}
	got := orderByFamily(in)
	if len(got) != 4 {
		t.Fatalf("不应丢地址：%v", got)
	}
	if got[0] != "104.26.12.1" || got[1] != "172.67.210.105" {
		t.Fatalf("v4 应排在前面：%v", got)
	}
	// 同族内保持原顺序。
	if got[2] != "2606:4700::6810:1234" || got[3] != "2001:db8::1" {
		t.Fatalf("同族顺序应保持：%v", got)
	}
}

// 纯 IPv6 环境不能被清空（有些教育网/云主机只有 AAAA）。
func TestOrderByFamilyKeepsV6Only(t *testing.T) {
	in := []string{"2001:db8::1", "2001:db8::2"}
	got := orderByFamily(in)
	if len(got) != 2 || got[0] != "2001:db8::1" {
		t.Fatalf("纯 v6 应原样返回：%v", got)
	}
}

// AETHER_HTTP2=0 时关闭 HTTP/2（给现场排障留的开关）。
func TestHTTP2EnvOverride(t *testing.T) {
	t.Setenv("AETHER_HTTP2", "")
	if !http2Enabled() {
		t.Fatal("默认应启用 HTTP/2")
	}
	for _, v := range []string{"0", "false", "off", "no"} {
		t.Setenv("AETHER_HTTP2", v)
		if http2Enabled() {
			t.Fatalf("AETHER_HTTP2=%q 应关闭 HTTP/2", v)
		}
	}
	t.Setenv("AETHER_HTTP2", "1")
	if !http2Enabled() {
		t.Fatal("AETHER_HTTP2=1 应启用")
	}
}
