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
	// Status 是上游返回的 HTTP 状态码；0 表示没拿到响应。
	Status int `json:"status,omitempty"`
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

// probeEdge 用真实 SNI 打一次 /cdn-cgi/trace（纯边缘响应），保留旧签名。
func probeEdge(ctx context.Context, host, ip string, timeout time.Duration) edgeCandidate {
	return probeEdgePath(ctx, host, ip, "/cdn-cgi/trace", timeout)
}

// probeEdgePath 用真实 SNI + Host 打一次指定路径，测出证书、机房与耗时。
//
// 两种口径：
//   - path="/cdn-cgi/trace"：纯边缘响应（不回源），用来判断 anycast 落到哪个机房；
//   - 其它路径（例如 open-design 的 /v1/models）：完整走「边缘 → 回源」，
//     测的是用户真实请求的链路。
//
// 为什么要区分：社区实测（open-design-ip-optimizer、DSH-CFHKoptimizer）发现
// amr-link.open-design.ai 的回源在美西，边缘挂 HKG 等于多绕一次跨太平洋往返——
// 只看 /cdn-cgi/trace 会把 HKG 选成最优，而真实请求反而比 SJC 慢 35%~42%。
// 所以"自动切换"必须允许用真实路径排序，不能只比边缘。
func probeEdgePath(ctx context.Context, host, ip, path string, timeout time.Duration) edgeCandidate {
	out := edgeCandidate{IP: ip}
	if strings.TrimSpace(path) == "" {
		path = "/cdn-cgi/trace"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
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
	req := "GET " + path + " HTTP/1.1\r\nHost: " + host +
		"\r\nUser-Agent: aether-edge-scan\r\nAccept: */*\r\nConnection: close\r\n\r\n"
	if _, err := tlsConn.Write([]byte(req)); err != nil {
		out.TTFBMS = time.Since(reqStart).Milliseconds()
		out.Error = "write: " + err.Error()
		return out
	}
	raw, err := io.ReadAll(io.LimitReader(tlsConn, 32<<10))
	out.TTFBMS = time.Since(reqStart).Milliseconds()
	if err != nil && len(raw) == 0 {
		out.Error = "read: " + err.Error()
		return out
	}

	status, ok := parseHTTPStatus(string(raw))
	if !ok {
		out.Error = "响应不是 HTTP"
		return out
	}
	out.Status = status
	body := splitHTTPBody(string(raw))

	// 边缘拒绝服务：TLS 与证书都正常，但拒绝为该 zone 提供服务。
	// 社区实测这类 IP 的响应比健康 IP 还快，只看耗时会把它排到第一，
	// 写进 hosts/固定表后用户侧随机 403 —— 必须按失败处理。
	if edgeBlockedResponse(status, body) {
		out.Error = "边缘拒绝服务（error code 10xx）"
		return out
	}
	if path == "/cdn-cgi/trace" {
		if status != http.StatusOK {
			out.Error = fmt.Sprintf("trace 返回 %d", status)
			return out
		}
		out.Colo = parseTraceColo(body)
		if out.Colo == "" {
			out.Colo = edgeColoUnknown
		}
	} else if status >= 500 {
		// 回源 5xx：链路未必坏，但对用户就是失败，不参与排序。
		out.Error = fmt.Sprintf("上游 %d", status)
		return out
	}
	out.OK = true
	return out
}

// parseHTTPStatus 从原始响应里取状态码。
func parseHTTPStatus(raw string) (int, bool) {
	line := raw
	if i := strings.Index(raw, "\r\n"); i >= 0 {
		line = raw[:i]
	}
	parts := strings.Fields(line)
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "HTTP/") {
		return 0, false
	}
	code, err := strconv.Atoi(parts[1])
	if err != nil || code < 100 || code > 599 {
		return 0, false
	}
	return code, true
}

// splitHTTPBody 返回头之后的正文。
func splitHTTPBody(raw string) string {
	if i := strings.Index(raw, "\r\n\r\n"); i >= 0 {
		return raw[i+4:]
	}
	return ""
}

// edgeBlockedResponse 判断边缘是否拒绝为该 zone 服务（HTTP 200 但 error code 10xx，
// 或 403 且带 Cloudflare 错误页标记）。注意：回源自己返回的 403 不在此列。
func edgeBlockedResponse(status int, body string) bool {
	low := strings.ToLower(body)
	if strings.Contains(low, "error code: 10") {
		return true
	}
	if status == http.StatusForbidden &&
		(strings.Contains(low, "cloudflare") || strings.Contains(low, "cf-error")) {
		return true
	}
	return false
}

// defaultEdgeCandidates 生成默认候选 IP。
//
// 三段轮流出点，固定步长（可复现，不随机）：
//   - 162.159.0.0/16、104.26.0.0/16：最常出香港/近端机房的段，按 /24 步长 4 抽样；
//   - 104.17.128-159：社区实测这一段是 SJC（圣何塞），对"回源在美西"的站点更快。
//
// 扫更多收益为零（能到近端的 IP 总量固定），所以限制总候选数而不是无限扩池。
func defaultEdgeCandidates(limit int) []string {
	if limit <= 0 {
		limit = 24
	}
	groups := []struct {
		prefix           string
		start, end, step int
	}{
		{"162.159.", 0, 256, 4},
		{"104.26.", 0, 256, 4},
		{"104.17.", 128, 160, 1},
	}
	out := make([]string, 0, limit)
	for idx := 0; len(out) < limit; idx++ {
		added := false
		for _, g := range groups {
			v := g.start + idx*g.step
			if v >= g.end {
				continue
			}
			added = true
			out = append(out, fmt.Sprintf("%s%d.1", g.prefix, v))
			if len(out) >= limit {
				break
			}
		}
		if !added {
			break
		}
	}
	return out
}

// scanEdges 并发探测候选 IP（纯边缘口径），按延迟升序返回。
func scanEdges(ctx context.Context, host string, candidates []string, concurrency int, timeout time.Duration) []edgeCandidate {
	return scanEdgesPath(ctx, host, "/cdn-cgi/trace", candidates, concurrency, timeout)
}

// scanEdgesPath 并发探测候选 IP 的指定路径，按可用优先、延迟升序返回。
func scanEdgesPath(ctx context.Context, host, path string, candidates []string, concurrency int, timeout time.Duration) []edgeCandidate {
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
			results[i] = probeEdgePath(ctx, host, ip, path, timeout)
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
		// 与 edge-pin 一致：写进设置，跨重启保留。
		g.settingsMu.Lock()
		if g.settings.EdgePins == nil {
			g.settings.EdgePins = map[string][]string{}
		}
		g.settings.EdgePins[host] = ips
		g.settingsMu.Unlock()
		_ = g.saveSettings()
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
	// 落盘：固定表要跨重启保留，否则用户每次启动都得重扫。
	g.settingsMu.Lock()
	if g.settings.EdgePins == nil {
		g.settings.EdgePins = map[string][]string{}
	}
	if ips := g.hostPins().Get(host); len(ips) > 0 {
		g.settings.EdgePins[host] = ips
	} else {
		delete(g.settings.EdgePins, host)
	}
	g.settingsMu.Unlock()
	_ = g.saveSettings()
	writeJSON(w, http.StatusOK, map[string]any{"host": host, "ips": g.hostPins().Get(host)})
}

// handleEdgePins GET /api/network/edge-pins —— 当前固定表。
func (g *Gateway) handleEdgePins(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"pins": g.hostPins().Snapshot()})
}
