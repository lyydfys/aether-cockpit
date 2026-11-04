package transport

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTP/2 降级：某些中转对 h2 支持不完整。
//
// 症状不是"连不上"，而是"偶发卡住"或直接 500：h2 连接能建立，但 stream 被
// 中间设备静默丢弃；Go 的 Transport 会把这个 host 的 h2 连接标记为坏，
// 后续请求继续撞在同一类错误上。
//
// 处理策略：第一次遇到 h2 专属错误时，**换一条禁用 h2 的 Transport 重试一次**，
// 并把这个 host 记成"h2 不可用"（10 分钟）。之后同 host 直接走 h1。
// 这样用户不需要理解 h2/ALPN，也不需要手动设 AETHER_HTTP2=0 全局关掉。

// h2BrokenHosts 记录被判为 h2 不可用的 host 与判定时间。
var h2BrokenHosts = struct {
	mu   sync.Mutex
	seen map[string]time.Time
}{seen: map[string]time.Time{}}

// h2BrokenTTL 是"该 host 不用 h2"的记忆时长。
//
// 取 10 分钟：太短会反复重试浪费一次往返，太长则上游修好后还得等。
const h2BrokenTTL = 10 * time.Minute

// hostH2Broken 判断该 host 是否近期 h2 失败过。
func hostH2Broken(host string) bool {
	h2BrokenHosts.mu.Lock()
	defer h2BrokenHosts.mu.Unlock()
	at, ok := h2BrokenHosts.seen[host]
	if !ok {
		return false
	}
	if time.Since(at) > h2BrokenTTL {
		delete(h2BrokenHosts.seen, host)
		return false
	}
	return true
}

// markHostH2Broken 记录该 host 的 h2 失败。
func markHostH2Broken(host string) {
	h2BrokenHosts.mu.Lock()
	defer h2BrokenHosts.mu.Unlock()
	h2BrokenHosts.seen[host] = time.Now()
}

// isH2Error 判断错误是否属于"HTTP/2 专属"问题。
//
// 只看与 h2 相关的字样：普通网络错误（超时、拒绝连接）重试 h1 没有意义，
// 反而会让一次失败变成两次。
func isH2Error(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, kw := range []string{
		"http2:", "http/2", "stream error", "goaway",
		"http_1_1_required", "frame too large", "protocol error",
		"connection reset by peer", // 常见于 h2 stream 被中间设备打断
	} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// h2Fallback 包一层 RoundTripper：h2 失败时用 h1 重试一次。
type h2Fallback struct {
	// primary 是启用 h2 的 Transport。
	primary *http.Transport
	// build 惰性构造"禁用 h2"的副本（拿到原 Transport 的完整配置）。
	build func(base *http.Transport) *http.Transport

	mu       sync.Mutex
	fallback *http.Transport
}

func newH2Fallback(base *http.Transport) *h2Fallback {
	return &h2Fallback{
		primary: base,
		build: func(b *http.Transport) *http.Transport {
			clone := b.Clone()
			clone.ForceAttemptHTTP2 = false
			// TLSNextProto 设为空 map 才会真正禁用 h2（只改 ForceAttemptHTTP2
			// 在已经协商过 h2 的连接池里不生效）。
			clone.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
			return clone
		},
	}
}

// h1Transport 返回（并缓存）禁用 h2 的副本。
func (h *h2Fallback) h1Transport() *http.Transport {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fallback == nil {
		h.fallback = h.build(h.primary)
	}
	return h.fallback
}

// RoundTrip 先按原 Transport 发；遇到 h2 专属错误则降级重试一次。
//
// 注意：只有"请求还没被消费"时才能重试。GET 与带 GetBody 的请求可以重放；
// 没有 GetBody 的流式 POST 不能重试（重发会丢 body），直接返回原错误。
func (h *h2Fallback) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	// 已知该 host 的 h2 有问题：直接走 h1，省掉一次注定失败的往返。
	if hostH2Broken(host) {
		return h.h1Transport().RoundTrip(req)
	}
	resp, err := h.primary.RoundTrip(req)
	if err == nil || !isH2Error(err) {
		return resp, err
	}
	// 响应体必须读完并关闭，否则连接无法复用（也会泄漏）。
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
	}
	// 请求不可重放（没有 GetBody 的 POST/PUT）→ 不重试。
	if req.Body != nil && req.GetBody == nil && req.Method != http.MethodGet && req.Method != http.MethodHead {
		return nil, err
	}
	markHostH2Broken(host)
	clone := req.Clone(req.Context())
	if req.GetBody != nil {
		body, gerr := req.GetBody()
		if gerr != nil {
			return nil, err
		}
		clone.Body = body
	}
	return h.h1Transport().RoundTrip(clone)
}

// UnwrapTransport 是 unwrapTransport 的导出版：其它包（测试/诊断）想检查
// 代理、连接池等配置时用，避免各自复制类型断言。
func UnwrapTransport(rt http.RoundTripper) *http.Transport { return unwrapTransport(rt) }

// unwrapTransport 从可能的包装层里取出底层的 *http.Transport。
//
// 测试与诊断需要看 Proxy/连接池等配置，而 h2Fallback 把原始 Transport 包了
// 一层；这里统一剥壳，避免每个调用方各写一次类型断言。
func unwrapTransport(rt http.RoundTripper) *http.Transport {
	if t, ok := rt.(*http.Transport); ok {
		return t
	}
	if f, ok := rt.(*h2Fallback); ok {
		return f.primary
	}
	return nil
}

var errH2FallbackDisabled = errors.New("h2 fallback disabled")
