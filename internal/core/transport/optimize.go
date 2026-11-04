package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// 链路优化：DoH 解析 + 多 IP 竞速。
//
// 面向的场景很具体：渠道挂在 Cloudflare 之类的 anycast 后面，国内直连时
// 不同边缘 IP 的质量差异极大——有的能跑、有的几乎不通。再加上本地 DNS
// 可能被污染/劫持，于是用户看到的都是"慢、卡、甚至不可用"。
//
// 这里做两件事，都只影响**建连**，不改协议、不改请求：
//
//	1. DoH 解析：绕开本地 DNS，拿到真实 A 记录并缓存 TTL。
//	2. IP 竞速：同时向多个候选 IP 发起 TCP 连接，谁先连上就用谁，
//	   并把赢家按 host 固定一段时间（pin），避免每次都重新竞速。
//
// 注意：TLS 仍由 http.Transport 用 URL 里的域名做 SNI——我们只替换了
// DialContext，所以证书校验与 SNI 行为完全不变。

// Optimizer 是链路优化器的配置与状态。
type Optimizer struct {
	// DoH 是解析服务地址；空表示只用系统解析。
	DoH string
	// Race 是否对同一域名的多个 IP 竞速。
	Race bool
	// MaxCandidates 最多同时尝试几个 IP。
	MaxCandidates int
	// PinTTL 是"赢家 IP"的固定时长。
	PinTTL time.Duration
	// TTL 是 DNS 缓存时长。
	TTL time.Duration

	mu    sync.Mutex
	cache map[string]dnsEntry
	pins  map[string]pinEntry
	// dial 可注入，便于测试竞速逻辑（默认走 net.Dialer）。
	dial func(ctx context.Context, network, addr string, timeout time.Duration) (net.Conn, error)
}

// dialTimeout 用默认拨号器建连。
func (o *Optimizer) dialWith(ctx context.Context, network, addr string, timeout time.Duration) (net.Conn, error) {
	if o != nil && o.dial != nil {
		return o.dial(ctx, network, addr, timeout)
	}
	return (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, addr)
}

type dnsEntry struct {
	ips []string
	at  time.Time
}

type pinEntry struct {
	ip string
	at time.Time
}

// NewOptimizer 创建优化器。
func NewOptimizer(doh string, race bool) *Optimizer {
	return &Optimizer{
		DoH:           strings.TrimSpace(doh),
		Race:          race,
		MaxCandidates: 4,
		PinTTL:        5 * time.Minute,
		TTL:           5 * time.Minute,
		cache:         map[string]dnsEntry{},
		pins:          map[string]pinEntry{},
	}
}

// Resolve 返回 host 的候选 IP（带缓存）。
//
// DoH 失败时退回系统解析——优化器不该成为新的故障点。
func (o *Optimizer) Resolve(ctx context.Context, host string) ([]string, error) {
	if o == nil {
		return net.DefaultResolver.LookupHost(ctx, host)
	}
	o.mu.Lock()
	if e, ok := o.cache[host]; ok && time.Since(e.at) < o.TTL {
		ips := append([]string(nil), e.ips...)
		o.mu.Unlock()
		return ips, nil
	}
	o.mu.Unlock()

	ips, err := o.lookupDoH(ctx, host)
	if err != nil || len(ips) == 0 {
		ips, err = net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			return nil, err
		}
	}
	o.mu.Lock()
	o.cache[host] = dnsEntry{ips: ips, at: time.Now()}
	o.mu.Unlock()
	return ips, nil
}

// lookupDoH 查 A 记录。
func (o *Optimizer) lookupDoH(ctx context.Context, host string) ([]string, error) {
	if o.DoH == "" {
		return nil, errors.New("未配置 DoH")
	}
	u := strings.TrimRight(o.DoH, "/")
	if !strings.Contains(u, "?") {
		u += "?name=" + host + "&type=A"
	}
	reqCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH 返回 %s", resp.Status)
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
	ips := make([]string, 0, len(doc.Answer))
	for _, a := range doc.Answer {
		if a.Type == 1 {
			ips = append(ips, a.Data)
		}
	}
	return ips, nil
}

// pinned 返回仍在有效期内的固定 IP。
func (o *Optimizer) pinned(host string) (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	p, ok := o.pins[host]
	if !ok || time.Since(p.at) >= o.PinTTL {
		return "", false
	}
	return p.ip, true
}

func (o *Optimizer) pin(host, ip string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pins[host] = pinEntry{ip: ip, at: time.Now()}
}

// DialContext 是替换 http.Transport.DialContext 的入口。
//
// 行为：
//   - 已 pin 且能连上 → 直连该 IP；
//   - 否则解析候选，竞速 TCP（不是竞速握手），取最快的那个并 pin 住；
//   - 全部失败返回最后一个错误。
func (o *Optimizer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	// IP 字面量不需要解析/竞速。
	if net.ParseIP(host) != nil {
		return o.dialWith(ctx, network, addr, 10*time.Second)
	}
	if ip, ok := o.pinned(host); ok {
		if c, derr := o.dialWith(ctx, network, net.JoinHostPort(ip, port), 4*time.Second); derr == nil {
			return c, nil
		}
		// pin 失效（IP 挂了）：清掉，走竞速。
		o.mu.Lock()
		delete(o.pins, host)
		o.mu.Unlock()
	}
	ips, err := o.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("解析 %s 没有结果", host)
	}
	if !o.Race || len(ips) == 1 {
		ip := ips[0]
		c, derr := o.dialWith(ctx, network, net.JoinHostPort(ip, port), 10*time.Second)
		if derr == nil {
			o.pin(host, ip)
		}
		return c, derr
	}

	max := o.MaxCandidates
	if max <= 0 || max > len(ips) {
		max = len(ips)
	}
	type result struct {
		conn net.Conn
		ip   string
		err  error
		ms   int64
	}
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan result, max)
	for _, ip := range ips[:max] {
		go func(ip string) {
			start := time.Now()
			c, derr := o.dialWith(raceCtx, network, net.JoinHostPort(ip, port), 5*time.Second)
			ch <- result{conn: c, ip: ip, err: derr, ms: time.Since(start).Milliseconds()}
		}(ip)
	}
	results := make([]result, 0, max)
	var firstErr error
	for i := 0; i < max; i++ {
		r := <-ch
		if r.err == nil {
			// 冠军到手：取消其余，关掉先到的落选连接。
			cancel()
			go func() {
				for j := i + 1; j < max; j++ {
					if extra := <-ch; extra.err == nil && extra.conn != nil {
						_ = extra.conn.Close()
					}
				}
			}()
			o.pin(host, r.ip)
			return r.conn, nil
		}
		if firstErr == nil {
			firstErr = r.err
		}
		results = append(results, r)
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("全部候选 IP 建连失败")
	}
	// 把最慢的那批信息附在错误里，便于诊断。
	sort.SliceStable(results, func(i, j int) bool { return results[i].ms < results[j].ms })
	return nil, fmt.Errorf("%s 建连失败（%d 个候选）：%w", host, len(results), firstErr)
}
