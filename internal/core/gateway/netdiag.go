package gateway

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// 网络诊断：回答"这个域名在我这台机器上到底卡在哪一环"。
//
// 为什么需要它：跨境/中转渠道的"卡"可能是四种完全不同的原因，处理方式
// 也完全不同，而用户看到的都只是"慢/打不开"：
//
//	1. DNS 被污染/劫持 → 解析到黑洞 IP（换 DoH 能解决）
//	2. 某个 IP 不可达      → 换 IP 能解决（多 IP 竞速）
//	3. TCP 通、TLS 被重置  → SNI 被拦（换域名/中转才行，应用层无解）
//	4. 都通但 RTT/丢包高   → 线路质量问题（只能减少握手次数、降并发）
//
// 这个端点把每一环的耗时与结果摊开，用户在自己（不挂代理）的网络里跑一次
// 就能定位到具体是哪一类，而不是只能凭感觉说"卡"。

// defaultDoHServer 是链路优化默认使用的 DoH 服务。
//
// 选阿里而不是 Cloudflare/Google：国内直连可达、无污染，且不会因为
// "优化器自己要翻墙"而变成新的故障点。
const defaultDoHServer = "https://dns.alidns.com/resolve"

// netDiagDoHTimeout 是单次 DoH 查询的超时。
const netDiagDoHTimeout = 4 * time.Second

// netDiagIPTimeout 是单个 IP 的 TCP+TLS+首字节探测总超时。
const netDiagIPTimeout = 6 * time.Second

// dohEndpoints 是诊断用的 DoH 服务（国内外各一个，结果可以互相印证）。
var dohEndpoints = []struct {
	name string
	url  string
}{
	{"alidns", "https://dns.alidns.com/resolve"},
	{"cloudflare", "https://cloudflare-dns.com/dns-query"},
}

// netDiagIPResult 是单个 IP 的探测结果。
type netDiagIPResult struct {
	IP         string `json:"ip"`
	OK         bool   `json:"ok"`
	Colo       string `json:"colo,omitempty"`
	TCPMS      int64  `json:"tcp_ms"`
	TCPError   string `json:"tcp_error,omitempty"`
	TLSMS      int64  `json:"tls_ms,omitempty"`
	TLSError   string `json:"tls_error,omitempty"`
	TTFBMS     int64  `json:"ttfb_ms,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	HTTPError  string `json:"http_error,omitempty"`
	Negotiated string `json:"tls_version,omitempty"`
	ALPN       string `json:"alpn,omitempty"`
	Verdict    string `json:"verdict"`
}

// netDiagResponse 是 /api/network/diag 的响应。
type netDiagResponse struct {
	Host        string              `json:"host"`
	Port        string              `json:"port"`
	SystemDNS   []string            `json:"system_dns"`
	SystemError string              `json:"system_dns_error,omitempty"`
	DoH         map[string][]string `json:"doh"`
	DoHErrors   map[string]string   `json:"doh_errors,omitempty"`
	IPs         []netDiagIPResult   `json:"ips"`
	Summary     string              `json:"summary"`
	ElapsedMS   int64               `json:"elapsed_ms"`
}

// handleNetDiag GET /api/network/diag?host=example.com[&port=443]
func (g *Gateway) handleNetDiag(w http.ResponseWriter, r *http.Request) {
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	if host == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 host 参数"})
		return
	}
	// 只接受域名/IP 字面量，避免被当成任意请求代理。
	if strings.ContainsAny(host, "/\\?&@ ") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "host 只能是域名或 IP"})
		return
	}
	port := strings.TrimSpace(r.URL.Query().Get("port"))
	if port == "" {
		port = "443"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, diagnoseHost(ctx, host, port))
}

// diagnoseHost 跑完整诊断（纯函数，便于测试）。
func diagnoseHost(ctx context.Context, host, port string) netDiagResponse {
	start := time.Now()
	out := netDiagResponse{Host: host, Port: port, DoH: map[string][]string{}}

	// 1. 本机系统 DNS（可能被 fake-ip 劫持，这本身就是线索）。
	out.SystemDNS, out.SystemError = systemLookup(ctx, host)

	// 2. 并行跑各 DoH，拿到"真实"答案用于对照。
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, ep := range dohEndpoints {
		wg.Add(1)
		go func(name, url string) {
			defer wg.Done()
			ips, err := dohLookup(ctx, name, url, host)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if out.DoHErrors == nil {
					out.DoHErrors = map[string]string{}
				}
				out.DoHErrors[name] = err.Error()
				return
			}
			out.DoH[name] = ips
		}(ep.name, ep.url)
	}
	wg.Wait()

	// 3. 汇总候选 IP：优先 DoH（真实），没有再用系统解析结果。
	seen := map[string]bool{}
	var candidates []string
	addAll := func(list []string) {
		for _, ip := range list {
			if ip == "" || seen[ip] {
				continue
			}
			// fake-ip 段（198.18.0.0/15）说明流量被本地代理接管，
			// 拿它做直连探测没有意义，单独标注。
			seen[ip] = true
			candidates = append(candidates, ip)
		}
	}
	for _, ep := range dohEndpoints {
		addAll(out.DoH[ep.name])
	}
	addAll(out.SystemDNS)

	// 最多测 4 个 IP：再多只是浪费用户时间。
	if len(candidates) > 4 {
		candidates = candidates[:4]
	}

	results := make([]netDiagIPResult, 0, len(candidates))
	var rmu sync.Mutex
	var iwg sync.WaitGroup
	for _, ip := range candidates {
		iwg.Add(1)
		go func(ip string) {
			defer iwg.Done()
			res := probeIP(ctx, host, port, ip)
			rmu.Lock()
			results = append(results, res)
			rmu.Unlock()
		}(ip)
	}
	iwg.Wait()

	// 快的排前面：用户最关心"哪个能用、哪个最快"。
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if (a.HTTPError == "" && a.TLSError == "") != (b.HTTPError == "" && b.TLSError == "") {
			return a.HTTPError == "" && a.TLSError == ""
		}
		return a.TTFBMS < b.TTFBMS
	})
	out.IPs = results
	out.ElapsedMS = time.Since(start).Milliseconds()
	out.Summary = summarizeDiag(out)
	return out
}

// systemLookup 走本机解析器（等价于浏览器看到的答案）。
func systemLookup(ctx context.Context, host string) ([]string, string) {
	if ip := net.ParseIP(host); ip != nil {
		return []string{host}, ""
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var ips []string
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return nil, err.Error()
	}
	for _, a := range addrs {
		ips = append(ips, a.IP.String())
	}
	return ips, ""
}

// dohLookup 用 DoH 查询 A 记录。
func dohLookup(ctx context.Context, name, endpoint, host string) ([]string, error) {
	u := endpoint + "?name=" + host + "&type=A"
	reqCtx, cancel := context.WithTimeout(ctx, netDiagDoHTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	client := &http.Client{Timeout: netDiagDoHTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return nil, &diagError{msg: name + " 返回 " + resp.Status}
	}
	var doc struct {
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var ips []string
	for _, a := range doc.Answer {
		if a.Type == 1 {
			ips = append(ips, a.Data)
		}
	}
	if len(ips) == 0 {
		return nil, &diagError{msg: name + " 没有 A 记录"}
	}
	return ips, nil
}

type diagError struct{ msg string }

func (e *diagError) Error() string { return e.msg }

// probeIP 依次测 TCP / TLS / 首个字节，任何一步失败都记下原因。
func probeIP(ctx context.Context, host, port, ip string) netDiagIPResult {
	res := netDiagIPResult{IP: ip}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(netDiagIPTimeout)
	}
	if d := time.Until(deadline); d > netDiagIPTimeout {
		deadline = time.Now().Add(netDiagIPTimeout)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	addr := net.JoinHostPort(ip, port)
	// 1. TCP
	t0 := time.Now()
	conn, err := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, "tcp", addr)
	res.TCPMS = time.Since(t0).Milliseconds()
	if err != nil {
		res.TCPError = err.Error()
		res.Verdict = "TCP 不可达：这个 IP 在当前网络下被拦或路由不通"
		return res
	}
	defer conn.Close()

	// 2. TLS（SNI 用真实域名——这能区分"IP 通但 SNI 被拦"）
	t1 := time.Now()
	tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	tlsConn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		res.TLSMS = time.Since(t1).Milliseconds()
		res.TLSError = err.Error()
		res.Verdict = "TCP 通但 TLS 握手失败：多半是 SNI 被拦或该 IP 的证书链被中断"
		return res
	}
	res.TLSMS = time.Since(t1).Milliseconds()
	res.Negotiated = tlsVersionName(tlsConn.ConnectionState().Version)
	res.ALPN = tlsConn.ConnectionState().NegotiatedProtocol

	// 3. 一个真实 HTTPS 请求的首字节（不带凭据，只看链路）。
	//
	// 打 /cdn-cgi/trace 而不是 /：它不回源站，测出来的是"到边缘"的真实延迟，
	// 而且响应里带 colo（机房代码），能直接看出 DNS 把你分到了哪里。
	t2 := time.Now()
	tlsConn.SetDeadline(time.Now().Add(6 * time.Second))
	req := "GET /cdn-cgi/trace HTTP/1.1\r\nHost: " + host + "\r\nConnection: close\r\nUser-Agent: aether-diag\r\n\r\n"
	if _, err := tlsConn.Write([]byte(req)); err != nil {
		res.TTFBMS = time.Since(t2).Milliseconds()
		res.HTTPError = err.Error()
		res.Verdict = "TLS 通但写请求失败：链路不稳"
		return res
	}
	raw, err := io.ReadAll(io.LimitReader(tlsConn, 16<<10))
	res.TTFBMS = time.Since(t2).Milliseconds()
	if err != nil && len(raw) == 0 {
		res.HTTPError = err.Error()
		res.Verdict = "TLS 通但收不到响应：链路被无响应丢弃"
		return res
	}
	body := string(raw)
	if i := strings.Index(body, "\r\n\r\n"); i >= 0 {
		res.Colo = parseTraceColo(body[i+4:])
	}
	if strings.HasPrefix(body, "HTTP/") {
		var code int
		if _, perr := fmtSscanf(body, &code); perr == nil {
			res.HTTPStatus = code
		}
	}
	res.OK = true
	res.Verdict = "可用"
	return res
}

// fmtSscanf 从 "HTTP/1.1 200 OK" 里取状态码。
func fmtSscanf(head string, code *int) (int, error) {
	fields := strings.Fields(head)
	if len(fields) < 2 {
		return 0, &diagError{msg: "状态行不完整"}
	}
	n := 0
	for _, r := range fields[1] {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	if n == 0 {
		return 0, &diagError{msg: "状态码解析失败"}
	}
	*code = n
	return 1, nil
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS1.3"
	case tls.VersionTLS12:
		return "TLS1.2"
	case tls.VersionTLS11:
		return "TLS1.1"
	case tls.VersionTLS10:
		return "TLS1.0"
	}
	return "unknown"
}

// summarizeDiag 给出一句人能读懂的结论。
//
// 这是这个端点最重要的部分：用户不该自己去对比四张表。
func summarizeDiag(r netDiagResponse) string {
	var usable []netDiagIPResult
	var tcpFail, tlsFail int
	for _, ip := range r.IPs {
		switch {
		case ip.TCPError != "":
			tcpFail++
		case ip.TLSError != "":
			tlsFail++
		default:
			usable = append(usable, ip)
		}
	}
	// fake-ip 说明本机有 TUN 类全局代理在接管流量，此时测到的都不是真实链路。
	fakeIP := false
	for _, ip := range r.SystemDNS {
		if strings.HasPrefix(ip, "198.18.") || strings.HasPrefix(ip, "198.19.") {
			fakeIP = true
		}
	}
	var b strings.Builder
	switch {
	case len(usable) > 0:
		best := usable[0]
		b.WriteString("直连可用：")
		b.WriteString(best.IP)
		b.WriteString("，TCP ")
		b.WriteString(itoaMS(best.TCPMS))
		b.WriteString("、TLS ")
		b.WriteString(itoaMS(best.TLSMS))
		b.WriteString("、首字节 ")
		b.WriteString(itoaMS(best.TTFBMS))
		if best.ALPN != "" {
			b.WriteString("，ALPN ")
			b.WriteString(best.ALPN)
		}
		if best.TTFBMS > 1500 {
			b.WriteString("。首字节超过 1.5s，属于线路慢，建议开连接预热并限制并发")
		}
	case tcpFail > 0 && tlsFail == 0:
		b.WriteString("所有候选 IP 的 TCP 都连不上：当前网络到该地址不通（可能需要代理，或换 DoH 解析后的其它 IP）")
	case tlsFail > 0:
		b.WriteString("TCP 通但 TLS 握手失败：典型的 SNI 拦截。应用层只能换线路/中转，无法绕过")
	default:
		b.WriteString("没有可用的解析结果，检查 DNS")
	}
	if fakeIP {
		b.WriteString("。注意：本机系统 DNS 返回 198.18.x（fake-ip），说明有 TUN 全局代理在接管，上面的直连数字不代表真实国内链路")
	}
	return b.String()
}

func itoaMS(v int64) string {
	if v <= 0 {
		return "—"
	}
	return itoa(v) + "ms"
}
