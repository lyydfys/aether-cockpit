// Package transport 是网关的网络底座。
//
// 它只做两件事，但都是"国内外渠道混跑时不互相污染"的关键：
//  1. 按渠道的 NetworkPolicy 为每个账号铸造独立的 http.Client（独立 CookieJar /
//     独立 TLS 会话缓存 / 独立连接池 / 独立出口）。
//  2. 在 direct_force 场景下正确绕过系统代理，并识别 TUN 模式这种绕不过去的情况。
package transport

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/provider"
)

// Factory 按网络策略铸造账号级 HTTP 客户端。
type Factory struct {
	// GlobalProxy 软件设置里的全局代理地址，如 socks5://127.0.0.1:7890。
	GlobalProxy string
	// InsecureSkipVerify 仅用于调试自签证书的上游。
	InsecureSkipVerify bool
	// DialTimeout 建连超时。
	DialTimeout time.Duration
	// EnableHTTP2 是否启用 HTTP/2。
	EnableHTTP2 bool
	// Optimizer 是链路优化器（DoH 解析 + 多 IP 竞速）；nil 表示不启用。
	//
	// 只影响建连：TLS/SNI/证书校验仍由 http.Transport 按域名完成。
	Optimizer *Optimizer

	mu sync.Mutex
	// proxyParsed 缓存解析结果，避免每次请求都解析 URL。
	proxyParsed *url.URL
	proxyErr    error
	proxyLoaded bool
}

// NewFactory 创建默认工厂。
func NewFactory(globalProxy string) *Factory {
	return &Factory{
		GlobalProxy: strings.TrimSpace(globalProxy),
		DialTimeout: 15 * time.Second,
		EnableHTTP2: http2Enabled(),
	}
}

// http2Enabled 读 AETHER_HTTP2 开关（默认开）。
//
// 为什么留这个口子：少数中转对 HTTP/2 支持不完整，表现为"偶发卡住"。
// 出问题时用户没法自己判断，只能等我们发版；给一个环境变量开关能在
// 现场立刻排除 h2 这个变量（设为 0 即全量走 HTTP/1.1）。
func http2Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AETHER_HTTP2"))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

func (f *Factory) globalProxyURL() (*url.URL, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.proxyLoaded {
		return f.proxyParsed, f.proxyErr
	}
	f.proxyLoaded = true
	if f.GlobalProxy == "" {
		return nil, nil
	}
	u, err := url.Parse(f.GlobalProxy)
	if err != nil {
		f.proxyErr = fmt.Errorf("transport: 全局代理地址无法解析 %q: %w", f.GlobalProxy, err)
		return nil, f.proxyErr
	}
	f.proxyParsed = u
	return u, nil
}

// SetGlobalProxy 在运行中切换全局代理；已建好的客户端不受影响，
// 调用方需要用新的 Factory 重新铸造账号客户端（pool.RebuildClients）。
func (f *Factory) SetGlobalProxy(proxy string) error {
	proxy = strings.TrimSpace(proxy)
	var u *url.URL
	if proxy != "" {
		parsed, err := url.Parse(proxy)
		if err != nil {
			return fmt.Errorf("transport: 全局代理地址无法解析 %q: %w", proxy, err)
		}
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("transport: 不支持的代理协议 %q（可用 http/https/socks5）", parsed.Scheme)
		}
		if parsed.Host == "" {
			return fmt.Errorf("transport: 代理地址缺少主机 %q", proxy)
		}
		u = parsed
	}
	f.mu.Lock()
	f.GlobalProxy = proxy
	f.proxyParsed = u
	f.proxyErr = nil
	f.proxyLoaded = true
	f.mu.Unlock()
	return nil
}

// GlobalProxyValue 返回当前全局代理（线程安全）。
func (f *Factory) GlobalProxyValue() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.GlobalProxy
}

// NewClient 为某个账号铸造独占的 http.Client。
//
// proxyOverride 用于"用户给单个账号显式指定了代理"的场景，优先级高于全局策略。
// tlsProfile 非空时启用 uTLS 指纹伪装（重放真实浏览器的 ClientHello）。
func (f *Factory) NewClient(policy provider.NetworkPolicy, proxyOverride, tlsProfile string) (*http.Client, error) {
	return f.NewClientOpts(policy, proxyOverride, tlsProfile, false)
}

// NewClientOpts 与 NewClient 相同，但可以按渠道跳过证书校验（渠道编辑器
// 高级 → Don't check its certificate）。insecure=true 时只影响这一个 client。
func (f *Factory) NewClientOpts(policy provider.NetworkPolicy, proxyOverride, tlsProfile string, insecure bool) (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	dialer := &net.Dialer{
		Timeout:   f.DialTimeout,
		KeepAlive: 30 * time.Second,
	}

	tr := &http.Transport{
		// 连接池按账号隔离：每个账号一个 Transport，天然不共享连接。
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
		DialContext:         dialer.DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: f.InsecureSkipVerify || insecure,
			MinVersion:         tls.VersionTLS12,
		},
		ForceAttemptHTTP2: f.EnableHTTP2,
		// 卡住读的保护：连上了但上游迟迟不给响应头时，不能让请求无限挂着。
		//
		// 典型场景是中转对 HTTP/2 支持不完整（h2 连接建立成功、stream 却被
		// 静默丢弃），表现为"偶发卡死"。有响应头超时后，请求会失败并进入
		// 正常的重试/换号路径，而不是一直转圈。
		ResponseHeaderTimeout: 60 * time.Second,
		// 关键：direct_force 下显式置 nil，彻底断开与 HTTP_PROXY 环境变量的任何联系。
		Proxy: nil,
	}

	switch policy {
	case provider.NetPolicyDirectForce:
		// 强制直连。Proxy 保持 nil，Go 的 Transport 便不会读取任何代理配置。
		// 注意：这里无法穿透 TUN 模式（见 DetectTUN 的说明），需要在 UI 层提示用户。
		tr.Proxy = nil

	case provider.NetPolicyProxyFirst:
		u, err := f.globalProxyURL()
		if err != nil {
			return nil, err
		}
		if u == nil {
			// 没配代理时退化为直连，但让调用方知道这是降级行为。
			tr.Proxy = nil
		} else {
			tr.Proxy = http.ProxyURL(u)
		}

	case provider.NetPolicySystem:
		// 先看 Windows「Internet 选项」里的系统代理，再退回环境变量。
		//
		// Go 的 ProxyFromEnvironment 只认 HTTP_PROXY/HTTPS_PROXY，而用户在
		// Clash / 公司代理里勾的"系统代理"只写注册表 —— 只读环境变量会
		// 表现为"浏览器能上网、网关直连超时"，这是最难被用户自查的问题。
		tr.Proxy = sysProxyFunc()

	default:
		tr.Proxy = sysProxyFunc()
	}

	// 单账号显式代理覆盖一切策略；"direct" 强制直连。
	if s := strings.TrimSpace(proxyOverride); s != "" {
		if strings.EqualFold(s, "direct") {
			tr.Proxy = nil
		} else {
			u, err := url.Parse(s)
			if err != nil {
				return nil, fmt.Errorf("transport: 账号代理地址无法解析 %q: %w", s, err)
			}
			tr.Proxy = http.ProxyURL(u)
		}
	}

	// 链路优化：替换建连逻辑（DoH 解析 + 多 IP 竞速）。
	// 只替换 DialContext，TLS/SNI/证书校验不变；走代理时对端是 127.0.0.1，
	// 优化器会识别为 IP 字面量直接放行，不产生额外解析。
	if f.Optimizer != nil {
		tr.DialContext = f.Optimizer.DialContext
	}

	// TLS 指纹伪装：只替换 TLS 层，代理与连接池策略不变。
	if strings.TrimSpace(tlsProfile) != "" {
		tr = newUTLSTransport(tr, tlsProfile, f.InsecureSkipVerify || insecure)
	}

	return &http.Client{
		Transport: tr,
		Jar:       jar,
		// 不设全局 Timeout：流式对话可能需要长时间保持连接。
		// 由 ctx 控制生命周期的做法更正确。
		Timeout: 0,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 8 {
				return errors.New("transport: 重定向次数过多")
			}
			return nil
		},
	}, nil
}

// RequestOption 用于给请求统一打上指纹头。
type RequestOption func(*http.Request)

// WithFingerprint 把账号指纹按渠道模板写入请求头。
//
// headersTemplate 支持 {{.Fingerprint.ClientVersion}} 这类占位符，
// 由各渠道的 spec 提供，从而做到"改版本号不用改代码"。
func WithFingerprint(fp provider.DeviceFingerprint, headersTemplate map[string]string) RequestOption {
	return func(r *http.Request) {
		for k, tmpl := range headersTemplate {
			v := expandFingerprint(tmpl, fp)
			if v == "" {
				continue
			}
			r.Header.Set(k, v)
		}
	}
}

// expandFingerprint 做极简占位符替换，避免为此引入 text/template 的开销。
func expandFingerprint(tmpl string, fp provider.DeviceFingerprint) string {
	repl := map[string]string{
		"{{.Fingerprint.MachineID}}":     fp.MachineID,
		"{{.Fingerprint.DeviceID}}":      fp.DeviceID,
		"{{.Fingerprint.MAC}}":           fp.MAC,
		"{{.Fingerprint.ClientVersion}}": fp.ClientVersion,
		"{{.Fingerprint.Platform}}":      fp.Platform,
	}
	out := tmpl
	for k, v := range repl {
		out = strings.ReplaceAll(out, k, v)
	}
	for k, v := range fp.Extra {
		out = strings.ReplaceAll(out, "{{.Fingerprint.Extra."+k+"}}", v)
	}
	return out
}
