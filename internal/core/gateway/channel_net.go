package gateway

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 渠道网络档位（慢链路优化）。
//
// 解决的问题很具体：跨境/中转类渠道有三类"卡"，而且都不在模型本身：
//
//  1. 冷连接贵：TLS 握手一次 0.2–1.5s。连接池空闲 90s 就回收，用户隔一会儿
//     发问，先白等一次握手。→ **预热**：定期打个不花额度的探针把连接保活。
//  2. 并发自伤：上游单请求就要 1s+ 时，20 个并发会互相排队，p95 直接爆掉。
//     → **并发闸**：按渠道限制在途数，超了短暂等待再放行（等不到也不阻塞，
//     宁可慢一点也不把请求卡死）。
//  3. 半死连接：链路丢包时请求会挂在已失效的连接上，直到超时（几十秒）。
//     → **首字节停滞重试**：迟迟没有首字节就换新连接重试一次。
//
// 这三项都按渠道配置，默认只对"远距离/中转"渠道启用，本地渠道保持原样。

// ChannelNetProfile 是一个渠道的网络档位。
type ChannelNetProfile struct {
	// PrewarmSec 是连接预热间隔（秒）；0 = 不预热。
	PrewarmSec int `json:"prewarm_sec,omitempty"`
	// MaxInflight 是该渠道的最大在途请求数；0 = 不限。
	MaxInflight int `json:"max_inflight,omitempty"`
	// StallTimeoutMS 是首字节停滞阈值（毫秒）；0 = 不检测。
	StallTimeoutMS int `json:"stall_timeout_ms,omitempty"`
}

// defaultChannelNet 是各渠道的默认档位（用户可在设置里覆盖）。
//
// 只给"链路远/经过中转"的渠道开预热与并发闸：本地或同城渠道开了反而
// 白白多打探针、多排队。
var defaultChannelNet = map[string]ChannelNetProfile{
	"open-design": {PrewarmSec: 45, MaxInflight: 4, StallTimeoutMS: 8000},
}

// channelNetStore 保存当前生效的档位（设置覆盖默认）。
type channelNetStore struct {
	mu       sync.RWMutex
	profiles map[string]ChannelNetProfile
}

func newChannelNetStore() *channelNetStore {
	s := &channelNetStore{profiles: map[string]ChannelNetProfile{}}
	for k, v := range defaultChannelNet {
		s.profiles[k] = v
	}
	return s
}

// Set 覆盖某渠道的档位（零值表示关闭该渠道的优化）。
func (s *channelNetStore) Set(pid string, p ChannelNetProfile) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p == (ChannelNetProfile{}) {
		delete(s.profiles, pid)
		return
	}
	s.profiles[pid] = p
}

// Get 返回某渠道的档位。
func (s *channelNetStore) Get(pid string) ChannelNetProfile {
	if s == nil {
		return ChannelNetProfile{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.profiles[pid]
}

// Snapshot 返回全部档位（诊断页用）。
func (s *channelNetStore) Snapshot() map[string]ChannelNetProfile {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]ChannelNetProfile, len(s.profiles))
	for k, v := range s.profiles {
		out[k] = v
	}
	return out
}

/* ---------------- 并发闸 ---------------- */

// inflightGate 是每渠道的在途请求闸门。
//
// 语义取舍：宁可"短暂等待后仍然放行"，也不做无限排队——网关的价值是转发，
// 因为排队把请求卡住比慢一点更糟。所以 acquire 有等待上限，超时就放行。
type inflightGate struct {
	mu    sync.Mutex
	sem   map[string]chan struct{}
	limit map[string]int
}

func newInflightGate() *inflightGate {
	return &inflightGate{sem: map[string]chan struct{}{}, limit: map[string]int{}}
}

// semaphore 取得（必要时重建）某渠道的信号量。
func (g *inflightGate) semaphore(pid string, limit int) chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	ch := g.sem[pid]
	if ch == nil || g.limit[pid] != limit {
		ch = make(chan struct{}, limit)
		g.sem[pid] = ch
		g.limit[pid] = limit
	}
	return ch
}

// acquire 尝试占一个槽位，最多等待 wait；超时则放行但不占槽。
// 返回的 release 必须调用一次（超时情况下是空操作）。
func (g *inflightGate) acquire(pid string, limit int, wait time.Duration) func() {
	if g == nil || limit <= 0 {
		return func() {}
	}
	ch := g.semaphore(pid, limit)
	select {
	case ch <- struct{}{}:
		return func() { <-ch }
	default:
	}
	if wait <= 0 {
		ch <- struct{}{}
		return func() { <-ch }
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }
	case <-timer.C:
		// 等到上限还没空位：放行但不占槽。慢一点，但绝不把请求卡死。
		return func() {}
	}
}

// inFlight 返回当前在途数（诊断用）。
func (g *inflightGate) inFlight(pid string) int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.sem[pid])
}

/* ---------------- 连接预热 ---------------- */

func (g *Gateway) channelNet() *channelNetStore {
	g.netOnce.Do(func() {
		if g.netProfiles == nil {
			g.netProfiles = newChannelNetStore()
		}
	})
	return g.netProfiles
}

// channelGate 返回在途闸门（惰性创建）。
func (g *Gateway) channelGate() *inflightGate {
	g.gateOnce.Do(func() {
		if g.gate == nil {
			g.gate = newInflightGate()
		}
	})
	return g.gate
}

// releaseWhenClosed 把"流结束"与"释放槽位"绑在一起。
//
// 为什么不直接 defer：槽位语义是"这条流还在跑"，而流在后台 goroutine 里
// 持续产生事件、openStream 早早返回了。只有转发器看到上游关闭，才说明
// 这条流真的结束了。
func releaseWhenClosed(in <-chan ir.UnifiedStreamEvent, release func()) <-chan ir.UnifiedStreamEvent {
	if release == nil {
		return in
	}
	out := make(chan ir.UnifiedStreamEvent, 8)
	go func() {
		defer close(out)
		defer release()
		for ev := range in {
			out <- ev
		}
	}()
	return out
}

// handleChannelNet GET /api/channels/net —— 当前渠道网络档位与在途数。
//
// 让"卡不卡"变成可观测的：用户能确认某个渠道到底有没有开优化、此刻几条流
// 在跑，而不是只能凭感觉。
func (g *Gateway) handleChannelNet(w http.ResponseWriter, r *http.Request) {
	profiles := g.channelNet().Snapshot()
	rows := make([]map[string]any, 0, len(profiles))
	for pid, p := range profiles {
		row := map[string]any{
			"provider_id":      pid,
			"prewarm_sec":      p.PrewarmSec,
			"max_inflight":     p.MaxInflight,
			"stall_timeout_ms": p.StallTimeoutMS,
			"inflight":         g.channelGate().inFlight(pid),
		}
		if m, ok := provider.Get(pid); ok {
			row["provider_name"] = m.Manifest().DisplayName
			row["base_url"] = m.Manifest().BaseURL
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": rows})
}

// warmProbeClient 返回该账号可用的 HTTP 客户端（没有就 nil）。
func warmProbeClient(env *provider.AccountEnv) *http.Client {
	if env == nil {
		return nil
	}
	return env.HTTPClient
}

// channelBaseURL 解析某账号实际使用的上游地址：
// 账号 base_url 优先，其次渠道清单里的默认地址。
func channelBaseURL(env *provider.AccountEnv, manifestBase string) string {
	if env != nil {
		if v := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/"); v != "" {
			return v
		}
	}
	return strings.TrimRight(strings.TrimSpace(manifestBase), "/")
}

// prewarmOnce 对某账号打一次轻量探针，把 TCP+TLS 连接保活。
//
// 探针用 GET /models：不消耗 token，几乎所有 OpenAI 兼容端点都有，
// 而且能顺带验证 Key 仍然有效。
func (g *Gateway) prewarmOnce(env *provider.AccountEnv, manifestBase string) {
	client := warmProbeClient(env)
	base := channelBaseURL(env, manifestBase)
	if client == nil || base == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return
	}
	if key := strings.TrimSpace(env.GetCredential("api_key")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return // 预热失败不影响任何请求，下一轮再试
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
}

// StartChannelWarm 启动连接预热巡检，返回停止函数。
//
// 每个渠道按自己的间隔各自计时；间隔不同说明需求不同（有的渠道希望
// 更热，有的只是不想冻结在冷启动上）。
func (g *Gateway) StartChannelWarm(ctx context.Context, tick time.Duration) func() {
	if tick <= 0 {
		tick = 15 * time.Second
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		last := map[string]time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-t.C:
				g.warmChannelsOnce(last)
			}
		}
	}()
	return func() { close(stop) }
}

// warmChannelsOnce 按各渠道的间隔决定这一轮要不要预热。
func (g *Gateway) warmChannelsOnce(last map[string]time.Time) {
	if g.pool == nil {
		return
	}
	now := time.Now()
	for pid, prof := range g.channelNet().Snapshot() {
		if prof.PrewarmSec <= 0 {
			continue
		}
		if t, ok := last[pid]; ok && now.Sub(t) < time.Duration(prof.PrewarmSec)*time.Second {
			continue
		}
		m, ok := provider.Get(pid)
		if !ok {
			continue
		}
		manifestBase := m.Manifest().BaseURL
		envs := g.pool.ListByProvider(pid)
		if len(envs) == 0 {
			continue
		}
		last[pid] = now
		// 只预热"最可能被用到"的那一个账号：全部预热会在多账号渠道上
		// 打出成倍的探针流量，收益却只有第一个热。
		for _, env := range envs {
			if env != nil && env.Available() {
				g.prewarmOnce(env, manifestBase)
				break
			}
		}
	}
}
