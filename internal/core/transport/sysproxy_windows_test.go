//go:build windows

package transport

import (
	"os"
	"testing"
)

// ProxyServer 的两种写法都要能解析成 url.URL。
func TestParseWinProxyServer(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"127.0.0.1:7890", "http://127.0.0.1:7890"},
		{" 127.0.0.1:7890 ", "http://127.0.0.1:7890"},
		{"http=127.0.0.1:7890;https=127.0.0.1:7891", "http://127.0.0.1:7890"},
		{"https=127.0.0.1:7891", "http://127.0.0.1:7891"},
		{"http://10.0.0.1:3128", "http://10.0.0.1:3128"},
		{"", ""},
		{"http=;https=", ""},
	}
	for _, tc := range cases {
		got := parseWinProxyServer(tc.in)
		if tc.want == "" {
			if got != nil {
				t.Errorf("parseWinProxyServer(%q) = %v，期望 nil", tc.in, got)
			}
			continue
		}
		if got == nil || got.String() != tc.want {
			t.Errorf("parseWinProxyServer(%q) = %v，期望 %s", tc.in, got, tc.want)
		}
	}
}

// ProxyOverride 的四种写法：精确、前缀通配、后缀通配、<local>。
func TestShouldBypass(t *testing.T) {
	patterns := []string{"localhost", "127.*", "*.internal", "example.com:", "<local>"}
	cases := map[string]bool{
		"localhost":       true,
		"127.0.0.1":       true,
		"api.internal":    true,
		"example.com":     false, // 注意：带冒号的写法在 WinINET 里是"端口通配"，这里不误判
		"intranet-host":   true,  // <local>：不带点的主机名
		"api.example.com": false,
	}
	for host, want := range cases {
		if got := shouldBypass(host, patterns); got != want {
			t.Errorf("shouldBypass(%q) = %v，期望 %v", host, got, want)
		}
	}
	// 没有匹配项时一律走代理。
	if shouldBypass("api.openai.com", []string{"localhost", "127.*"}) {
		t.Error("未命中任何规则时不应绕过")
	}
	// 空名单不过滤。
	if shouldBypass("api.openai.com", nil) {
		t.Error("空名单不应绕过")
	}
}

// 读不到注册表（或系统代理关闭）时必须退回环境变量，不能让 Proxy 变成 nil
// 从而静默直连——那正是"用户勾了系统代理却连不上"的成因。
func TestSysProxyFuncFallsBack(t *testing.T) {
	fn := sysProxyFunc()
	if fn == nil {
		t.Fatal("sysProxyFunc 不应返回 nil")
	}
	// 直接调用不 panic 即可：真实环境里读注册表可能因权限失败。
	if _, err := fn(mustRequest(t, "http://example.invalid/")); err != nil {
		t.Fatalf("不应返回错误：%v", err)
	}
}

// 本机真实注册表读数的端到端校验。
//
// 默认跳过：CI 机器上的系统代理状态是不确定的，把机器状态写进断言会让
// 测试随环境红绿。需要验证时手动开：
//
//	AETHER_SYSPROXY_E2E=1 go test ./internal/core/transport/ -run TestReadWinINetProxyLocal -v
func TestReadWinINetProxyLocal(t *testing.T) {
	if os.Getenv("AETHER_SYSPROXY_E2E") == "" {
		t.Skip("未设置 AETHER_SYSPROXY_E2E，跳过本机注册表校验")
	}
	proxy, bypass := readWinINetProxy()
	t.Logf("系统代理 = %v，绕过名单 %d 条", proxy, len(bypass))
	if proxy == nil {
		t.Fatal("本机已开启系统代理，但没读出来")
	}
	// 网关自己的回环地址必须能命中绕过规则，否则本机请求会被绕去代理。
	if !shouldBypass("127.0.0.1", bypass) {
		t.Errorf("127.0.0.1 应命中绕过规则，实际名单：%v", bypass)
	}
}
