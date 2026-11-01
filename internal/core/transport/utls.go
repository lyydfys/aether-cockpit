package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
)

// tlsProfiles 把 spec 里的 profile 名映射到 uTLS 的 ClientHello 指纹。
//
// 为什么需要这一层：上游风控会看 TLS 握手的 JA3/JA4。标准库的 ClientHello
// 有非常固定的特征（扩展顺序、GREASE 值、加密套件顺序），一眼就能看出"这不是官方客户端"。
// uTLS 通过重放真实浏览器的 ClientHello 来消除这个特征。
//
// 版本对应关系不必完全精确：uTLS 的 HelloChrome_120 与真实 Chrome 124 的
// JA3 已经高度接近，足以通过绝大多数指纹校验。
var tlsProfiles = map[string]utls.ClientHelloID{
	"chrome_auto":   utls.HelloChrome_Auto,
	"chrome_120":    utls.HelloChrome_120,
	"chrome_131":    utls.HelloChrome_131,
	"chrome_133":    utls.HelloChrome_133,
	"chrome_124":    utls.HelloChrome_131, // 无精确版本时用最近的
	"firefox_auto":  utls.HelloFirefox_Auto,
	"firefox_120":   utls.HelloFirefox_120,
	"safari_auto":   utls.HelloSafari_Auto,
	"edge_auto":     utls.HelloEdge_Auto,
	"ios_auto":      utls.HelloIOS_Auto,
	"android_okhttp": utls.HelloAndroid_11_OkHttp,
	"randomized":    utls.HelloRandomized,
}

// ResolveTLSProfile 把配置里的名字解析成 uTLS 指纹 ID。
//
// 未知名字回退到 Chrome Auto 而不是报错：
// "配置写错了"不应该让整个渠道不可用，用一个合理的默认指纹更稳妥。
func ResolveTLSProfile(name string) (utls.ClientHelloID, bool) {
	if id, ok := tlsProfiles[strings.ToLower(strings.TrimSpace(name))]; ok {
		return id, true
	}
	return utls.HelloChrome_Auto, false
}

// KnownTLSProfiles 返回支持的 profile 名（供 UI 展示与校验）。
func KnownTLSProfiles() []string {
	out := make([]string, 0, len(tlsProfiles))
	for k := range tlsProfiles {
		out = append(out, k)
	}
	return out
}

// utlsDialer 用指定的 ClientHello 指纹建立 TLS 连接。
type utlsDialer struct {
	profile   utls.ClientHelloID
	timeout   time.Duration
	insecure  bool
	alpn      []string
}

// DialTLSContext 实现 http.Transport 的 DialTLSContext 签名。
func (d *utlsDialer) DialTLSContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// 先建 TCP 连接。注意这里用的是裸 dialer：代理场景下 Go 会先在
	// 代理上建隧道，再由本函数做 TLS，两者不冲突。
	rawDialer := &net.Dialer{Timeout: d.timeout, KeepAlive: 30 * time.Second}
	rawConn, err := rawDialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	cfg := &utls.Config{
		ServerName:         host,
		InsecureSkipVerify: d.insecure,
		NextProtos:         d.alpn,
		// 与真实浏览器一致：优先 TLS 1.3。
		MinVersion: tls.VersionTLS12,
	}

	conn := utls.UClient(rawConn, cfg, d.profile)
	if err := conn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("transport: uTLS 握手失败（profile=%s）: %w", d.profile.Str(), err)
	}
	return conn, nil
}

// newUTLSTransport 构造一个使用 uTLS 指纹的 Transport。
//
// 只替换 TLS 层，代理、连接池、超时策略与标准 Transport 完全一致，
// 这样"要不要伪装指纹"就变成一个可开关的参数，而不是两套代码路径。
func newUTLSTransport(base *http.Transport, profile string, insecure bool) *http.Transport {
	if base == nil {
		base = &http.Transport{}
	}
	tr := base.Clone()

	id, _ := ResolveTLSProfile(profile)
	d := &utlsDialer{
		profile:  id,
		timeout:  15 * time.Second,
		insecure: insecure,
		alpn:     []string{"h2", "http/1.1"},
	}
	tr.DialTLSContext = d.DialTLSContext
	// 用自定义 DialTLS 时必须自己管 TLS，关掉 Transport 的内置 TLS 配置。
	tr.TLSClientConfig = nil
	// uTLS 自己处理 ALPN，Go 会从 ConnectionState 里读到协商结果。
	tr.ForceAttemptHTTP2 = true
	return tr
}
