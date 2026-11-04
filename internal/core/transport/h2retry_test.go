package transport

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// h2 专属错误要能识别；普通网络错误不能被误判（否则一次失败变两次）。
func TestIsH2Error(t *testing.T) {
	h2 := []error{
		errors.New("http2: server sent GOAWAY"),
		errors.New("stream error: stream ID 3; INTERNAL_ERROR"),
		errors.New("http2: frame too large"),
		errors.New("HTTP_1_1_REQUIRED"),
		errors.New("protocol error: unexpected EOF"),
		errors.New("read tcp: connection reset by peer"),
	}
	for _, err := range h2 {
		if !isH2Error(err) {
			t.Errorf("%v 应判为 h2 问题", err)
		}
	}
	plain := []error{
		nil,
		errors.New("dial tcp 1.2.3.4:443: i/o timeout"),
		errors.New("no such host"),
		errors.New("context canceled"),
	}
	for _, err := range plain {
		if isH2Error(err) {
			t.Errorf("%v 不该判为 h2 问题", err)
		}
	}
}

// 被判坏的 host 在 TTL 内要记住，避免每次都先撞一次 h2。
func TestH2BrokenHostMemory(t *testing.T) {
	host := "h2-broken.example.com"
	if hostH2Broken(host) {
		t.Fatal("初始不应为坏")
	}
	markHostH2Broken(host)
	if !hostH2Broken(host) {
		t.Fatal("标记后应判定为坏")
	}
	// 别的 host 不受影响。
	if hostH2Broken("other.example.com") {
		t.Fatal("不该波及其它 host")
	}
}

// 端到端：h2 失败的主 Tranport + h1 可用的回退，请求应该成功返回。
func TestH2FallbackRetriesOverHTTP1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	base := &http.Transport{}
	markHostH2Broken("127.0.0.1") // 让它直接走 h1 分支，验证链路可用

	fb := newH2Fallback(base)
	resp, err := fb.RoundTrip(mustRequest(t, srv.URL))
	if err != nil {
		t.Fatalf("回退请求失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("状态 = %d", resp.StatusCode)
	}
	// h1 副本必须真的禁用了 h2。
	h1 := fb.h1Transport()
	if h1.ForceAttemptHTTP2 || len(h1.TLSNextProto) != 0 {
		t.Fatalf("h1 副本没有禁用 h2：%+v", h1.TLSNextProto)
	}
}

// 剥壳函数要能从包装里取回原始 Transport（测试与诊断依赖它）。
func TestUnwrapTransport(t *testing.T) {
	base := &http.Transport{}
	if got := unwrapTransport(base); got != base {
		t.Fatal("裸 Transport 应原样返回")
	}
	fb := newH2Fallback(base)
	if got := unwrapTransport(fb); got != base {
		t.Fatal("包装层应剥回原始 Transport")
	}
	if got := unwrapTransport(nil); got != nil {
		t.Fatal("nil 应返回 nil")
	}
}
