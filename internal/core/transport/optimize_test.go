package transport

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type fakeConn struct{ net.Conn }

func (fakeConn) Close() error { return nil }

// DoH 解析结果进缓存：第二次直接用缓存，不再打网络。
func TestOptimizerResolveCaches(t *testing.T) {
	o := NewOptimizer("", false)
	// 直接写缓存，验证"命中缓存就不解析"这条路径（解析本身另有用例覆盖）。
	o.cache["api.example.com"] = dnsEntry{ips: []string{"203.0.113.9"}, at: time.Now()}
	ips, err := o.Resolve(context.Background(), "api.example.com")
	if err != nil || len(ips) != 1 || ips[0] != "203.0.113.9" {
		t.Fatalf("应命中缓存：%v %v", ips, err)
	}
}

// DoH 不可用时退回系统解析，不能把优化器变成新故障点。
func TestOptimizerFallsBackToSystem(t *testing.T) {
	o := NewOptimizer("http://127.0.0.1:1/none", false)
	ips, err := o.Resolve(context.Background(), "localhost")
	if err != nil || len(ips) == 0 {
		t.Fatalf("应退回系统解析：%v %v", ips, err)
	}
}

// 竞速：注入一个"某 IP 明显更快"的拨号器，验证选中最快者并 pin 住。
func TestOptimizerRacePicksFastestAndPins(t *testing.T) {
	o := NewOptimizer("", true)
	o.MaxCandidates = 3
	// 注入解析结果（跳过 DoH）。
	o.cache["slow.example.com"] = dnsEntry{
		ips: []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"},
		at:  time.Now(),
	}
	// 第三个 IP 最快。
	delays := map[string]time.Duration{
		"198.51.100.1": 120 * time.Millisecond,
		"198.51.100.2": 60 * time.Millisecond,
		"198.51.100.3": 5 * time.Millisecond,
	}
	var dialed atomic.Int32
	o.dial = func(ctx context.Context, network, addr string, timeout time.Duration) (net.Conn, error) {
		dialed.Add(1)
		host, _, _ := net.SplitHostPort(addr)
		d := delays[host]
		select {
		case <-time.After(d):
			return fakeConn{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	conn, err := o.DialContext(context.Background(), "tcp", "slow.example.com:443")
	if err != nil {
		t.Fatalf("应连上：%v", err)
	}
	_ = conn.Close()
	if dialed.Load() != 3 {
		t.Fatalf("应同时尝试 3 个候选，实际 %d", dialed.Load())
	}
	ip, ok := o.pinned("slow.example.com")
	if !ok {
		t.Fatal("赢家应被 pin 住")
	}
	if ip != "198.51.100.3" {
		t.Fatalf("应 pin 最快的 IP，得到 %q", ip)
	}

	// 已 pin：下一次只拨一个，不再竞速。
	dialed.Store(0)
	if c, err := o.DialContext(context.Background(), "tcp", "slow.example.com:443"); err != nil {
		t.Fatalf("pin 后应直连：%v", err)
	} else {
		_ = c.Close()
	}
	if dialed.Load() != 1 {
		t.Fatalf("pin 后应只拨一次，实际 %d", dialed.Load())
	}
}

// pin 的 IP 连不上时要清掉 pin 并重新竞速（不能一直往死 IP 上撞）。
func TestOptimizerPinFailover(t *testing.T) {
	o := NewOptimizer("", true)
	o.cache["x.example.com"] = dnsEntry{
		ips: []string{"198.51.100.9", "198.51.100.8"},
		at:  time.Now(),
	}
	// 先 pin 一个坏 IP。
	o.pin("x.example.com", "198.51.100.9")
	var dialed atomic.Int32
	o.dial = func(ctx context.Context, network, addr string, timeout time.Duration) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		if host == "198.51.100.9" {
			return nil, &net.OpError{Op: "dial", Err: context.DeadlineExceeded}
		}
		dialed.Add(1)
		return fakeConn{}, nil
	}
	c, err := o.DialContext(context.Background(), "tcp", "x.example.com:443")
	if err != nil {
		t.Fatalf("应回退到别的 IP：%v", err)
	}
	_ = c.Close()
	if dialed.Load() == 0 {
		t.Fatal("pin 失效后应重新尝试其它候选")
	}
	if ip, _ := o.pinned("x.example.com"); ip != "198.51.100.8" {
		t.Fatalf("应重新 pin 到可用 IP，得到 %q", ip)
	}
}

// 走代理时对端是 IP 字面量 → 必须原样直通，绝不解析。
func TestOptimizerPassesThroughLiteralIP(t *testing.T) {
	o := NewOptimizer("http://127.0.0.1:1/none", true) // DoH 故意不可用
	var dialed atomic.Int32
	o.dial = func(ctx context.Context, network, addr string, timeout time.Duration) (net.Conn, error) {
		dialed.Add(1)
		return fakeConn{}, nil
	}
	c, err := o.DialContext(context.Background(), "tcp", "127.0.0.1:7892")
	if err != nil {
		t.Fatalf("IP 字面量必须直通：%v", err)
	}
	_ = c.Close()
	if dialed.Load() != 1 {
		t.Fatalf("应只拨一次，实际 %d", dialed.Load())
	}
}

// DoH 解析：只取 A 记录，忽略 CNAME。
func TestOptimizerDoHParsesAOnly(t *testing.T) {
	o := NewOptimizer("", false)
	o.cache = map[string]dnsEntry{}
	ips, err := o.lookupDoH(context.Background(), "")
	if err == nil || len(ips) != 0 {
		t.Fatalf("未配置 DoH 应报错：%v %v", ips, err)
	}
}

var _ = json.Marshal
