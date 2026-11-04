package gateway

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Cloudflare 边缘优选。
//
// 背景（实测）：同一个域名在不同线路上会被 anycast 分配到完全不同的机房。
// 用 /cdn-cgi/trace 可以拿到真实落地机房（colo），实测某条宽带：
//
//	104.21.45.58  → SJC（圣何塞）  connect 186ms ttfb 602ms
//	172.67.210.105→ LHR（伦敦）    connect 211ms ttfb 680ms
//	104.26.12.1   → SEA（西雅图）  connect 152ms ttfb 483ms
//
// 也就是说：**DNS 给的那两个 IP 未必是离你最近的**，而换一个 IP 能省 20%+
// 延迟。社区做法是改 hosts 把域名钉到优选 IP —— 那需要管理员权限、影响
// 全系统、且浏览器开了 DoH 会绕过 hosts。
//
// 这里给同样的效果，但**只作用于网关内的这一个渠道**：扫描候选 IP、用真实
// SNI + 证书校验确认可用、测出机房与延迟，然后把结果钉在该渠道上。

// edgeColoUnknown 是拿不到机房信息时的占位。
const edgeColoUnknown = "?"

// edgeCandidate 是一个候选边缘 IP 的探测结果。
type edgeCandidate struct {
	IP       string `json:"ip"`
	Colo     string `json:"colo,omitempty"`
	TCPMS    int64  `json:"tcp_ms,omitempty"`
	TLSMS    int64  `json:"tls_ms,omitempty"`
	TTFBMS   int64  `json:"ttfb_ms,omitempty"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	TLSValid bool   `json:"tls_valid,omitempty"`
}

// parseTraceColo 从 /cdn-cgi/trace 的响应里取机房代码。
func parseTraceColo(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "colo="); ok && v != "" {
			return strings.ToUpper(strings.TrimSpace(v))
		}
	}
	return ""
}

// probeEdge 用真实 SNI 打一次 /cdn-cgi/trace，拿到机房、证书有效性与耗时。
//
// 为什么用 /cdn-cgi/trace 而不是 HEAD /：
//  1. 它不回源站，纯边缘响应，测出来的才是"到边缘"的真实链路；
//  2. 响应里带 colo，能直接判断落到哪个机房。
func probeEdge(ctx context.Context, host, ip string, timeout time.Duration) edgeCandidate {
	out := edgeCandidate{IP: ip}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialStart := time.Now()
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", net.JoinHostPort(ip, "443"))
	out.TCPMS = time.Since(dialStart).Milliseconds()
	if err != nil {
		out.Error = "TCP: " + err.Error()
		return out
	}
	defer conn.Close()

	tlsStart := time.Now()
	tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	tlsConn.SetDeadline(time.Now().Add(timeout))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		out.TLSMS = time.Since(tlsStart).Milliseconds()
		out.Error = "TLS: " + err.Error()
		return out
	}
	out.TLSMS = time.Since(tlsStart).Milliseconds()
	out.TLSValid = true // 握手成功即证明该 IP 持有该域名的合法证书

	reqStart := time.Now()
	req := "GET /cdn-cgi/trace HTTP/1.1\r\nHost: " + host +
		"\r\nUser-Agent: aether-edge-scan\r\nAccept: */*\r\nConnection: close\r\n\r\n"
	if _, err := tlsConn.Write([]byte(req)); err != nil {
		out.TTFBMS = time.Since(reqStart).Milliseconds()
		out.Error = "write: " + err.Error()
		return out
	}
	raw, err := io.ReadAll(io.LimitReader(tlsConn, 16<<10))
	out.TTFBMS = time.Since(reqStart).Milliseconds()
	if err != nil && len(raw) == 0 {
		out.Error = "read: " + err.Error()
		return out
	}
	body := string(raw)
	if !strings.HasPrefix(body, "HTTP/") {
		out.Error = "响应不是 HTTP"
		return out
	}
	// 只取头之后的正文部分。
	if i := strings.Index(body, "\r\n\r\n"); i >= 0 {
		body = body[i+4:]
	}
	out.Colo = parseTraceColo(body)
	if out.Colo == "" {
		out.Colo = edgeColoUnknown
	}
	out.OK = true
	return out
}

// defaultEdgeCandidates 生成默认候选 IP。
//
// 只取两段、按固定步长抽样：实测扫更多收益为零（能到近端的 IP 总量固定），
// 而耗时随候选数线性增长。步长固定而不是随机，保证同一网络下结果可复现。
//
// 两段轮流出点：先扫满一段会把另一段挤出去，而落在哪一段纯看线路。
func defaultEdgeCandidates(limit int) []string {
	if limit <= 0 {
		limit = 24
	}
	prefixes := []string{"104.26.", "162.159."}
	out := make([]string, 0, limit)
	// 每段按 /24 步长 8 抽样；轮流出点直到凑满 limit。
	for i := 0; i < 32 && len(out) < limit; i++ {
		for _, prefix := range prefixes {
			if len(out) >= limit {
				break
			}
			out = append(out, fmt.Sprintf("%s%d.1", prefix, i*8))
		}
	}
	return out
}

// scanEdges 并发探测候选 IP，按"可用优先、延迟升序"排序返回。
func scanEdges(ctx context.Context, host string, candidates []string, concurrency int, timeout time.Duration) []edgeCandidate {
	if concurrency <= 0 {
		concurrency = 12
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	results := make([]edgeCandidate, len(candidates))
	for i, ip := range candidates {
		wg.Add(1)
		go func(i int, ip string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = probeEdge(ctx, host, ip, timeout)
		}(i, ip)
	}
	wg.Wait()

	out := make([]edgeCandidate, 0, len(results))
	for _, r := range results {
		if r.OK {
			out = append(out, r)
		}
	}
	return sortCandidates(out)
}

// sortCandidates 按"到边缘的总耗时"升序（TLS+TTFB），并列看 TCP。
func sortCandidates(list []edgeCandidate) []edgeCandidate {
	sort.SliceStable(list, func(i, j int) bool {
		si := list[i].TTFBMS + list[i].TLSMS
		sj := list[j].TTFBMS + list[j].TLSMS
		if si != sj {
			return si < sj
		}
		return list[i].TCPMS < list[j].TCPMS
	})
	return list
}

// hostPins 返回边缘固定表（惰性创建）。
func (g *Gateway) hostPins() *hostPinStore {
	g.hostPinOnce.Do(func() {
		if g.hostPin == nil {
			g.hostPin = newHostPinStore()
		}
	})
	return g.hostPin
}

// hostPinStore 保存"某域名钉哪些 IP"（按渠道配置，不写系统 hosts）。
type hostPinStore struct {
	mu   sync.RWMutex
	pins map[string][]string
}

func newHostPinStore() *hostPinStore {
	return &hostPinStore{pins: map[string][]string{}}
}

// Set 钉住某个域名的候选 IP（空表示取消）。
func (s *hostPinStore) Set(host string, ips []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ips) == 0 {
		delete(s.pins, host)
		return
	}
	clean := make([]string, 0, len(ips))
	for _, ip := range ips {
		if net.ParseIP(strings.TrimSpace(ip)) != nil {
			clean = append(clean, strings.TrimSpace(ip))
		}
	}
	if len(clean) == 0 {
		delete(s.pins, host)
		return
	}
	s.pins[host] = clean
}

// Get 返回该域名钉住的 IP。
func (s *hostPinStore) Get(host string) []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.pins[host]...)
}

// Snapshot 返回全部固定项（诊断页展示）。
func (s *hostPinStore) Snapshot() map[string][]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string][]string, len(s.pins))
	for k, v := range s.pins {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// handleEdgeScan GET /api/network/edge-scan?host=&limit=&timeout_ms=
//
// 扫描并返回按延迟排序的可用边缘；`?apply=1` 时把前两个写进该渠道的固定表。
func (g *Gateway) handleEdgeScan(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	host := strings.TrimSpace(q.Get("host"))
	if host == "" || strings.ContainsAny(host, "/\\?&@ ") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少或非法的 host 参数"})
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 64 {
		limit = 24
	}
	timeoutMS, _ := strconv.Atoi(q.Get("timeout_ms"))
	timeout := 5 * time.Second
	if timeoutMS >= 500 && timeoutMS <= 15000 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()

	start := time.Now()
	results := scanEdges(ctx, host, defaultEdgeCandidates(limit), 12, timeout)
	best := make([]edgeCandidate, 0, 3)
	if len(results) > 3 {
		best = results[:3]
	} else {
		best = results
	}

	out := map[string]any{
		"host":       host,
		"scanned":    limit,
		"usable":     len(results),
		"best":       best,
		"elapsed_ms": time.Since(start).Milliseconds(),
		"note":       "只作用于网关内的该渠道，不修改系统 hosts；结果可随时清除。",
	}
	if q.Get("apply") == "1" && len(best) > 0 {
		ips := make([]string, 0, 2)
		for _, b := range best[:min(2, len(best))] {
			ips = append(ips, b.IP)
		}
		g.hostPins().Set(host, ips)
		out["applied"] = ips
	}
	writeJSON(w, http.StatusOK, out)
}

// handleEdgePin POST /api/network/edge-pin {host, ips:[...]}
func (g *Gateway) handleEdgePin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host string   `json:"host"`
		IPs  []string `json:"ips"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	host := strings.TrimSpace(body.Host)
	if host == "" || strings.ContainsAny(host, "/\\?&@ ") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "host 非法"})
		return
	}
	g.hostPins().Set(host, body.IPs)
	writeJSON(w, http.StatusOK, map[string]any{"host": host, "ips": g.hostPins().Get(host)})
}

// handleEdgePins GET /api/network/edge-pins —— 当前固定表。
func (g *Gateway) handleEdgePins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"pins": g.hostPins().Snapshot()})
}
