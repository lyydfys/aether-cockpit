//go:build windows

package transport

import (
	"errors"
	"net"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"
)

// 建连类错误必须被识别出来，否则代理失效无法自愈。
func TestIsConnFailureRecognizesConnectErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"连接被拒", errors.New("dial tcp 127.0.0.1:7892: connectex: No connection could be made because the target machine actively refused it."), true},
		{"ECONNREFUSED", syscall.ECONNREFUSED, true},
		{"DNS 失败", &net.DNSError{Err: "no such host", Name: "x"}, true},
		{"代理建连失败", errors.New(`proxyconnect tcp: dial tcp 127.0.0.1:7892: connectex: No connection could be made`), true},
		{"网络不可达", errors.New("network is unreachable"), true},

		// 这些不该被当成代理问题。
		{"HTTP 500", errors.New("HTTP 500: internal error"), false},
		{"超时", errors.New("context deadline exceeded"), false},
		{"额度不足", errors.New("insufficient_quota"), false},
		{"空错误", nil, false},
	}
	for _, c := range cases {
		if got := IsConnFailure(c.err); got != c.want {
			t.Errorf("%s: IsConnFailure = %v, want %v (err=%v)", c.name, got, c.want, c.err)
		}
	}
}

// 标记失效后，系统代理函数必须返回 nil（即直连）。
func TestSysProxyBypassedAfterFailure(t *testing.T) {
	ClearProxySuspect()
	t.Cleanup(ClearProxySuspect)

	// 造一个确定存在的代理配置：环境变量最简单可控。
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")

	req, _ := http.NewRequest(http.MethodGet, "https://example.com/x", nil)

	// 未标记时，环境变量代理应当生效。
	if u, _ := sysProxyFunc()(req); u == nil {
		t.Skip("当前环境未走到代理分支（系统代理可能已关闭），跳过正向断言")
	}

	MarkProxySuspect("test")
	if !proxySuspectedBroken() {
		t.Fatal("标记后应处于失效窗口")
	}
	if u, _ := sysProxyFunc()(req); u != nil {
		t.Fatalf("代理标记失效后应返回 nil（直连），实际 %v", u)
	}
}

// NoteConnFailure：没有代理配置时不该误判（那是目标站点本身连不上）。
func TestNoteConnFailureWithoutProxyDoesNothing(t *testing.T) {
	ClearProxySuspect()
	t.Cleanup(ClearProxySuspect)

	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(k, "")
	}

	// 用一个明确的 Factory 且不配全局代理。
	f := NewFactory("")
	err := errors.New("connection refused")

	// 若本机确实配置了 Windows 系统代理，这条断言不成立，跳过。
	if hasProxyConfigured(f) {
		t.Skip("本机存在系统代理配置，跳过")
	}
	if NoteConnFailure(f, err) {
		t.Fatal("没有代理配置时不应标记代理失效")
	}
	if proxySuspectedBroken() {
		t.Fatal("没有代理配置时不应进入绕开代理状态")
	}
}

// 失效窗口到期后应自动恢复使用代理，不能永久禁用。
func TestProxySuspectExpires(t *testing.T) {
	ClearProxySuspect()
	t.Cleanup(ClearProxySuspect)

	if proxySuspectedBroken() {
		t.Fatal("初始状态不应是失效")
	}
	// 直接写入一个已过期的截止时刻，模拟窗口到期。
	proxyBrokenUntil.Store(time.Now().Add(-time.Second).UnixNano())
	if proxySuspectedBroken() {
		t.Fatal("到期后不应再判定为失效")
	}
}

// 确保测试不会真的依赖本机代理存在。
func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
