package gateway

// 边缘线路的**自动切换**。
//
// 背景：社区两个开源项目（open-design-ip-optimizer 的常驻模式、DSH-CFHKoptimizer 插件）
// 证明了同一件事——把域名钉到更优的 Cloudflare 边缘 IP，能把 open-design 这条链路
// 的请求成功率从 12/20 提到 20/20、首 token 延迟减半。它们的做法是改写系统 hosts，
// 因此需要管理员权限、影响全系统、浏览器开 DoH 还会绕过。
//
// 我们已经有等价骨架（DoH 解析 + 竞速 + 手动「扫描并钉住」，只作用于网关内的某个
// 渠道、不碰系统 hosts）。缺的是它们最关键的一半：**持续监测 + 自动切换**。
//
// 这里补齐：
//  1. 定期用「真实路径」测当前钉住的线路（默认 /cdn-cgi/trace；可按渠道填 /v1/models
//     这类回源路径，避免出现"边缘很快、回源绕远"的误判）；
//  2. 定期全量扫描候选（含社区实测的 SJC 段），先淘汰再排序；
//  3. 带迟滞切换：只有新线路比当前快出阈值（默认 150ms）才换；当前线路全挂时立即换。
//
// 判断口径对齐社区结论：`degraded`（有失败样本）一票否决 → 先保证"不选到坏 IP"，
// 再在可用线路之间比速度；只比边缘不比回源会选错机房。

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
)

// EdgeAutoConfig 是某个域名的自动切换配置。
type EdgeAutoConfig struct {
	Enabled bool `json:"enabled"`
	// Path 是实测路径；空 = /cdn-cgi/trace（纯边缘口径）。
	// 回源在远端（例如 open-design 的美西源站）时应填真实 API 路径。
	Path string `json:"path,omitempty"`
	// IntervalSec 是全量重扫间隔（秒），默认 300。
	IntervalSec int `json:"interval_sec,omitempty"`
	// ThresholdMS 是切换阈值（毫秒），默认 150。
	ThresholdMS int `json:"threshold_ms,omitempty"`
	// HealthSec 是"当前线路健康采样"的间隔（秒），默认 30、最小 5。
	// 采样越密越快发现劣化，但探测流量也越大。
	HealthSec int `json:"health_sec,omitempty"`
}

func (c EdgeAutoConfig) interval() time.Duration {
	sec := c.IntervalSec
	if sec <= 0 {
		sec = 300
	}
	if sec < 60 {
		sec = 60
	}
	return time.Duration(sec) * time.Second
}

func (c EdgeAutoConfig) healthInterval() time.Duration {
	sec := c.HealthSec
	if sec <= 0 {
		sec = 30
	}
	if sec < 5 {
		sec = 5
	}
	if sec > 300 {
		sec = 300
	}
	return time.Duration(sec) * time.Second
}

func (c EdgeAutoConfig) thresholdMS() int64 {
	ms := c.ThresholdMS
	if ms <= 0 {
		ms = 150
	}
	return int64(ms)
}

// EdgeAutoState 是自动切换的运行态（给界面看）。
type EdgeAutoState struct {
	Host       string   `json:"host"`
	Enabled    bool     `json:"enabled"`
	Path       string   `json:"path,omitempty"`
	CurrentIPs []string `json:"current_ips,omitempty"`
	// CurrentMS 是当前线路的实测中位数（毫秒）；CurrentOK 是成功样本数。
	CurrentMS  int64  `json:"current_ms,omitempty"`
	CurrentOK  int    `json:"current_ok"`
	BestIP     string `json:"best_ip,omitempty"`
	BestMS     int64  `json:"best_ms,omitempty"`
	Usable     int    `json:"usable"`
	Scanned    int    `json:"scanned"`
	Switches   int    `json:"switches"`
	LastRun    int64  `json:"last_run,omitempty"`
	LastSwitch int64  `json:"last_switch,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Error      string `json:"error,omitempty"`

	// 健康监测：两次全量扫描之间的高频采样结果与告警。
	Degraded   bool        `json:"degraded,omitempty"`
	HealthMS   int64       `json:"health_ms,omitempty"`
	BaselineMS int64       `json:"baseline_ms,omitempty"`
	LastAlert  int64       `json:"last_alert,omitempty"`
	Alerts     []EdgeAlert `json:"alerts,omitempty"`

	// 真实请求观测：用网关自己的首 token 时间校准，而不是只看探针。
	LiveMS       int64 `json:"live_ms,omitempty"`
	LiveBaseline int64 `json:"live_baseline_ms,omitempty"`
	LiveSamples  int   `json:"live_samples,omitempty"`
}

// EdgeAlert 是一条线路告警（劣化/恢复），给界面与事件流用。
type EdgeAlert struct {
	At      int64  `json:"at"`
	Level   string `json:"level"` // warn | info
	Message string `json:"message"`
}

type edgeAutoRuntime struct {
	mu    sync.Mutex
	state map[string]*EdgeAutoState
}

var edgeAuto = &edgeAutoRuntime{state: map[string]*EdgeAutoState{}}

// edgeHealthTrack 是「高频健康采样」的滚动窗口。
//
// 全量扫描间隔默认 5 分钟，太慢，链路中途劣化时用户早就卡住了。这里用 30 秒一次
// 的单次采样做劣化检测：连续失败、或明显慢于自身基线时告警，并立刻触发一次切换。
type edgeHealthTrack struct {
	mu        sync.Mutex
	samples   []int64 // 最近的成功样本（毫秒）
	fails     int     // 连续失败次数
	lastProbe time.Time
	lastScan  time.Time

	// liveSamples 是"真实请求首 token 时间"的滚动窗口（最多 8 个）。
	liveSamples    []int64
	liveSlowStreak int
}

var edgeHealth = struct {
	mu sync.Mutex
	m  map[string]*edgeHealthTrack
}{m: map[string]*edgeHealthTrack{}}

func healthTrackFor(host string) *edgeHealthTrack {
	edgeHealth.mu.Lock()
	defer edgeHealth.mu.Unlock()
	t := edgeHealth.m[host]
	if t == nil {
		t = &edgeHealthTrack{}
		edgeHealth.m[host] = t
	}
	return t
}

// appendEdgeAlert 往状态里追加一条告警（最多保留 10 条，新的在前）。
func appendEdgeAlert(st *EdgeAutoState, level, msg string, at int64) {
	if at == 0 {
		at = time.Now().Unix()
	}
	alert := EdgeAlert{At: at, Level: level, Message: msg}
	st.Alerts = append([]EdgeAlert{alert}, st.Alerts...)
	if len(st.Alerts) > 10 {
		st.Alerts = st.Alerts[:10]
	}
	st.LastAlert = at
}

// medianInts 返回中位数（入参会被排序副本，不改原切片）。
func medianInts(vals []int64) int64 {
	if len(vals) == 0 {
		return 0
	}
	cp := append([]int64(nil), vals...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	return cp[len(cp)/2]
}

// checkEdgeHealth 采样一次当前线路，必要时告警并触发切换。
//
// 返回 true 表示本轮判定为"劣化且已触发处置"（供测试与日志使用）。
func (g *Gateway) checkEdgeHealth(ctx context.Context, host string, cfg EdgeAutoConfig) bool {
	ips := g.hostPins().Get(host)
	if len(ips) == 0 {
		return false
	}
	t := healthTrackFor(host)
	t.mu.Lock()
	t.lastProbe = time.Now()
	lastScan := t.lastScan
	t.mu.Unlock()

	probe := g.edgeProbe(ctx, host, ips[0], cfg.Path, 6*time.Second)
	st := edgeAuto.snapshot(host)
	st.Host, st.Enabled, st.Path = host, cfg.Enabled, strings.TrimSpace(cfg.Path)

	// 连续失败：链路基本不可用，立即告警并触发切换。
	if !probe.OK {
		t.mu.Lock()
		t.fails++
		fails := t.fails
		t.mu.Unlock()
		if fails >= 2 && !st.Degraded {
			st.Degraded = true
			appendEdgeAlert(&st, "warn", "当前线路连续失败（"+probe.Error+"），正在尝试切换", time.Now().Unix())
			edgeAuto.update(st)
			eventbus.Default().Publish("network.edge_degraded", map[string]any{
				"host": host, "reason": probe.Error, "fails": fails,
			})
			g.triggerEdgeAutoSwitch(ctx, host, cfg, time.Since(lastScan))
			return true
		}
		st.HealthMS = 0
		edgeAuto.update(st)
		return false
	}

	// 成功样本：更新窗口与基线。
	t.mu.Lock()
	t.fails = 0
	t.samples = append(t.samples, probe.TTFBMS)
	if len(t.samples) > 5 {
		t.samples = t.samples[len(t.samples)-5:]
	}
	samples := append([]int64(nil), t.samples...)
	t.mu.Unlock()

	st.HealthMS = probe.TTFBMS
	if len(samples) >= 3 {
		// 基线取"除当前这次以外"的历史中位数，避免把劣化值混进基线。
		st.BaselineMS = medianInts(samples[:len(samples)-1])
	}

	slow := st.BaselineMS > 0 &&
		probe.TTFBMS > st.BaselineMS+max(200, st.BaselineMS/2)

	if !st.Degraded && slow {
		st.Degraded = true
		appendEdgeAlert(&st, "warn",
			fmt.Sprintf("当前线路变慢：%dms（基线 %dms），正在评估切换", probe.TTFBMS, st.BaselineMS),
			time.Now().Unix())
		edgeAuto.update(st)
		eventbus.Default().Publish("network.edge_degraded", map[string]any{
			"host": host, "now_ms": probe.TTFBMS, "baseline_ms": st.BaselineMS,
		})
		g.triggerEdgeAutoSwitch(ctx, host, cfg, time.Since(lastScan))
		return true
	}
	if st.Degraded && !slow && len(samples) >= 2 {
		st.Degraded = false
		appendEdgeAlert(&st, "info",
			fmt.Sprintf("当前线路已恢复：%dms（基线 %dms）", probe.TTFBMS, st.BaselineMS),
			time.Now().Unix())
		edgeAuto.update(st)
		eventbus.Default().Publish("network.edge_recovered", map[string]any{
			"host": host, "now_ms": probe.TTFBMS, "baseline_ms": st.BaselineMS,
		})
		return false
	}
	edgeAuto.update(st)
	return false
}

// triggerEdgeAutoSwitch 在劣化时立刻跑一轮完整扫描（有最小间隔，避免打爆上游）。
func (g *Gateway) triggerEdgeAutoSwitch(ctx context.Context, host string, cfg EdgeAutoConfig, sinceScan time.Duration) {
	t := healthTrackFor(host)
	t.mu.Lock()
	if time.Since(t.lastScan) < 45*time.Second {
		t.mu.Unlock()
		return
	}
	t.lastScan = time.Now()
	t.mu.Unlock()

	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		st := g.runEdgeAutoOnce(runCtx, host, cfg)
		t.mu.Lock()
		t.lastScan = time.Now()
		t.mu.Unlock()
		if len(st.CurrentIPs) > 0 && st.CurrentIPs[0] != "" {
			cur := edgeAuto.snapshot(host)
			appendEdgeAlert(&cur, "info", "已应用新线路 "+strings.Join(st.CurrentIPs, ", "), time.Now().Unix())
			edgeAuto.update(cur)
		}
	}()
	_ = ctx
}

func (r *edgeAutoRuntime) snapshot(host string) EdgeAutoState {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.state[host]; s != nil {
		return *s
	}
	return EdgeAutoState{Host: host}
}

func (r *edgeAutoRuntime) update(st EdgeAutoState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := st
	r.state[st.Host] = &cp
}

// decideEdgeSwitch 判断是否切换线路（纯函数，便于测试）。
//
// 规则（对齐社区经验）：
//   - 当前线路没有任何成功样本 → 立即切（它已经不可用了）；
//   - 新线路没有可用样本 → 不切；
//   - 当前线路样本不足（<2）说明抖动大 → 只要新线路更快就切；
//   - 否则只有快出阈值才切，避免在两条相近线路之间来回跳。
func decideEdgeSwitch(currentMS int64, currentOK int, bestMS int64, thresholdMS int64) (bool, string) {
	if bestMS <= 0 {
		return false, "没有可用候选"
	}
	if currentOK <= 0 {
		return true, "当前线路全部失败，切到新线路"
	}
	if currentOK < 2 {
		if bestMS < currentMS {
			return true, "当前线路不稳定，切到更快的线路"
		}
		return false, "当前线路不稳定但没有更快的候选"
	}
	if bestMS+thresholdMS < currentMS {
		return true, "新线路快 " + strconv.FormatInt(currentMS-bestMS, 10) + "ms（阈值 " +
			strconv.FormatInt(thresholdMS, 10) + "ms）"
	}
	return false, "新线路优势不足阈值，保持当前线路"
}

// medianMS 返回成功样本的中位数；无成功样本返回 0。
func medianMS(samples []edgeCandidate) (int64, int) {
	vals := make([]int64, 0, len(samples))
	for _, s := range samples {
		if s.OK {
			vals = append(vals, s.TTFBMS)
		}
	}
	if len(vals) == 0 {
		return 0, 0
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	return vals[len(vals)/2], len(vals)
}

// measureHost 对一组已钉住的 IP 各采样 n 次，返回中位数与成功样本数。
func (g *Gateway) measureHost(ctx context.Context, host string, ips []string, path string, n int) (int64, int) {
	if len(ips) == 0 || n <= 0 {
		return 0, 0
	}
	all := make([]edgeCandidate, 0, len(ips)*n)
	for _, ip := range ips {
		for i := 0; i < n; i++ {
			all = append(all, g.edgeProbe(ctx, host, ip, path, 6*time.Second))
		}
	}
	return medianMS(all)
}

// scanEdgeCandidates 全量扫描候选并按真实路径排序。
func (g *Gateway) scanEdgeCandidates(ctx context.Context, host, path string, limit int) []edgeCandidate {
	if limit <= 0 {
		limit = 96
	}
	candidates := defaultEdgeCandidates(limit)
	return g.edgeScan(ctx, host, path, candidates, 16, 4*time.Second)
}

// runEdgeAutoOnce 执行一轮「测当前 → 扫候选 → 决定是否切换」。
func (g *Gateway) runEdgeAutoOnce(ctx context.Context, host string, cfg EdgeAutoConfig) EdgeAutoState {
	host = strings.TrimSpace(host)
	path := strings.TrimSpace(cfg.Path)
	st := edgeAuto.snapshot(host)
	st.Host = host
	st.Enabled = cfg.Enabled
	st.Path = path

	cur := g.hostPins().Get(host)
	st.CurrentIPs = cur

	curMS, curOK := g.measureHost(ctx, host, cur, path, 3)
	st.CurrentMS, st.CurrentOK = curMS, curOK

	best := g.scanEdgeCandidates(ctx, host, path, 96)
	st.Scanned = 96
	st.Usable = len(best)
	st.LastRun = time.Now().Unix()

	if len(best) == 0 {
		st.Error = "没有可用候选（全部探测失败）"
		st.Reason = "保持现状"
		edgeAuto.update(st)
		return st
	}

	// 对前 3 名做 3 次复测，避免单次抖动决定线路。
	top := best
	if len(top) > 3 {
		top = top[:3]
	}
	var pick edgeCandidate
	var pickMS int64
	for _, c := range top {
		ms, ok := g.measureHost(ctx, host, []string{c.IP}, path, 3)
		if ok == 0 {
			continue
		}
		if pickMS == 0 || ms < pickMS {
			pick, pickMS = c, ms
		}
	}
	if pickMS == 0 {
		st.Error = "候选复测全部失败"
		st.Reason = "保持现状"
		edgeAuto.update(st)
		return st
	}
	st.BestIP, st.BestMS = pick.IP, pickMS

	doSwitch, reason := decideEdgeSwitch(curMS, curOK, pickMS, cfg.thresholdMS())
	st.Reason = reason
	if doSwitch {
		ips := []string{pick.IP}
		// 第二名做备用：DialContext 会在它们之间竞速，主 IP 挂掉时自动降级。
		for _, c := range best {
			if c.IP != pick.IP {
				ips = append(ips, c.IP)
				break
			}
		}
		g.hostPins().Set(host, ips)
		g.persistEdgePins(host, ips)
		st.CurrentIPs = ips
		st.CurrentMS = pickMS
		st.CurrentOK = 3
		st.Switches++
		st.LastSwitch = time.Now().Unix()
		// 切完后重置健康窗口：旧基线已经不代表新线路。
		t := healthTrackFor(host)
		t.mu.Lock()
		t.samples = nil
		t.fails = 0
		t.mu.Unlock()
	}
	edgeAuto.update(st)
	return st
}

// persistEdgePins 把固定表写回 settings.json（跨重启保留）。
func (g *Gateway) persistEdgePins(host string, ips []string) {
	g.settingsMu.Lock()
	if g.settings.EdgePins == nil {
		g.settings.EdgePins = map[string][]string{}
	}
	if len(ips) == 0 {
		delete(g.settings.EdgePins, host)
	} else {
		g.settings.EdgePins[host] = ips
	}
	g.settingsMu.Unlock()
	_ = g.saveSettings()
}

// edgeAutoConfig 读取某域名的自动切换配置。
func (g *Gateway) edgeAutoConfig(host string) EdgeAutoConfig {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return g.settings.EdgeAuto[host]
}

// edgeAutoHosts 返回已开启自动切换的域名（写设置时保持稳定顺序）。
func (g *Gateway) edgeAutoHosts() map[string]EdgeAutoConfig {
	out := map[string]EdgeAutoConfig{}
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	for host, cfg := range g.settings.EdgeAuto {
		if cfg.Enabled && strings.TrimSpace(host) != "" {
			out[host] = cfg
		}
	}
	return out
}

// StartEdgeAuto 启动自动切换循环；返回停止函数（可重复调用，nil 安全）。
func (g *Gateway) StartEdgeAuto(ctx context.Context, tick time.Duration) func() {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		timer := time.NewTicker(tick)
		defer timer.Stop()
		var lastRun = map[string]time.Time{}
		var lastHealth = map[string]time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			for host, cfg := range g.edgeAutoHosts() {
				// 高频健康采样：30 秒一次单探针，用于「劣化即时告警」。
				if time.Since(lastHealth[host]) >= cfg.healthInterval() {
					lastHealth[host] = time.Now()
					hCtx, hCancel := context.WithTimeout(ctx, 20*time.Second)
					go func(host string, cfg EdgeAutoConfig) {
						defer hCancel()
						g.checkEdgeHealth(hCtx, host, cfg)
					}(host, cfg)
				}
				// 全量扫描：按域名自己的 interval（默认 300 秒）。
				if t, ok := lastRun[host]; ok && time.Since(t) < cfg.interval() {
					continue
				}
				lastRun[host] = time.Now()
				runCtx, runCancel := context.WithTimeout(ctx, 120*time.Second)
				go func(host string, cfg EdgeAutoConfig) {
					defer runCancel()
					g.runEdgeAutoOnce(runCtx, host, cfg)
				}(host, cfg)
			}
		}
	}()
	return cancel
}

// handleEdgeAuto GET /api/network/edge-auto —— 列表 + 运行态。
func (g *Gateway) handleEdgeAuto(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		g.settingsMu.Lock()
		cfgs := map[string]EdgeAutoConfig{}
		for host, cfg := range g.settingsEdgeAutoLocked() {
			cfgs[host] = cfg
		}
		g.settingsMu.Unlock()

		out := make([]any, 0, len(cfgs))
		for host, cfg := range cfgs {
			st := edgeAuto.snapshot(host)
			st.Enabled = cfg.Enabled
			st.Path = cfg.Path
			out = append(out, map[string]any{
				"config": cfg,
				"state":  st,
				"pins":   g.hostPins().Get(host),
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"hosts": out})
		return
	}

	var body struct {
		Host        string `json:"host"`
		Enabled     *bool  `json:"enabled"`
		Path        string `json:"path"`
		IntervalSec int    `json:"interval_sec"`
		ThresholdMS int    `json:"threshold_ms"`
		HealthSec   int    `json:"health_sec"`
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
	cfg := g.edgeAutoConfig(host)
	if body.Enabled != nil {
		cfg.Enabled = *body.Enabled
	}
	cfg.Path = strings.TrimSpace(body.Path)
	if body.IntervalSec > 0 {
		cfg.IntervalSec = body.IntervalSec
	}
	if body.ThresholdMS > 0 {
		cfg.ThresholdMS = body.ThresholdMS
	}
	if body.HealthSec > 0 {
		cfg.HealthSec = body.HealthSec
	}

	g.settingsMu.Lock()
	if g.settings.EdgeAuto == nil {
		g.settings.EdgeAuto = map[string]EdgeAutoConfig{}
	}
	g.settings.EdgeAuto[host] = cfg
	g.settingsMu.Unlock()
	_ = g.saveSettings()

	if cfg.Enabled {
		// 注意：不能用请求的 ctx —— 响应返回后它会被取消，探测会被打断。
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			g.runEdgeAutoOnce(ctx, host, cfg)
		}()
	}
	writeJSON(w, http.StatusOK, map[string]any{"host": host, "config": cfg, "state": edgeAuto.snapshot(host)})
}

// settingsEdgeAutoLocked 返回设置里的 EdgeAuto（调用方需持锁；nil 安全）。
func (g *Gateway) settingsEdgeAutoLocked() map[string]EdgeAutoConfig {
	if g.settings.EdgeAuto == nil {
		return map[string]EdgeAutoConfig{}
	}
	return g.settings.EdgeAuto
}

// edgeProbeFunc / edgeScanFunc 允许测试注入假探测，避免单测打真实网络。
type edgeProbeFunc func(ctx context.Context, host, ip, path string, timeout time.Duration) edgeCandidate
type edgeScanFunc func(ctx context.Context, host, path string, candidates []string, concurrency int, timeout time.Duration) []edgeCandidate

// noteEdgeLiveTTFT 用一次真实请求的首 token 时间校准线路健康度。
//
// 为什么需要它：探针测的是"网络往返"，而用户体感是"首 token 时间"，后者里
// 90% 是上游模型时间（社区两个项目也验证了这一点）。只用探针会导致两种误判：
//  1. 上游变慢时我们毫无感知（探针仍然很快）；
//  2. 上游偶尔思考久一点被当成线路劣化（探针没变但 TTFT 变高）。
//
// 所以这里单独维护一条"真实请求"基线，并且用更宽的阈值（>2x 且多 800ms），
// 连续两次才升级为告警——避免把模型自身的思考波动算到线路上。
func (g *Gateway) noteEdgeLiveTTFT(providerID string, ttftMS int64) {
	if ttftMS <= 0 {
		return
	}
	p, ok := provider.Get(providerID)
	if !ok {
		return
	}
	host := hostOfBaseURL(p.Manifest().BaseURL)
	if host == "" {
		return
	}
	cfg := g.edgeAutoConfig(host)
	if !cfg.Enabled {
		return
	}

	t := healthTrackFor(host)
	t.mu.Lock()
	t.liveSamples = append(t.liveSamples, ttftMS)
	if len(t.liveSamples) > 8 {
		t.liveSamples = t.liveSamples[len(t.liveSamples)-8:]
	}
	samples := append([]int64(nil), t.liveSamples...)
	t.mu.Unlock()

	st := edgeAuto.snapshot(host)
	st.LiveMS = ttftMS
	st.LiveSamples = len(samples)
	var baseline int64
	if len(samples) >= 3 {
		baseline = medianInts(samples[:len(samples)-1])
		st.LiveBaseline = baseline
	} else {
		baseline = st.LiveBaseline
	}

	if baseline <= 0 {
		edgeAuto.update(st)
		return
	}
	slow := ttftMS > baseline*2 && ttftMS > baseline+800

	t.mu.Lock()
	if slow {
		t.liveSlowStreak++
	} else {
		t.liveSlowStreak = 0
	}
	streak := t.liveSlowStreak
	t.mu.Unlock()

	if slow && streak >= 2 && !st.Degraded {
		st.Degraded = true
		appendEdgeAlert(&st, "warn",
			fmt.Sprintf("真实请求首 token 变慢：%dms（基线 %dms），正在评估切换", ttftMS, baseline),
			time.Now().Unix())
		edgeAuto.update(st)
		eventbus.Default().Publish("network.edge_degraded", map[string]any{
			"host": host, "live_ms": ttftMS, "live_baseline_ms": baseline, "source": "live_request",
		})
		g.triggerEdgeAutoSwitch(context.Background(), host, cfg, 0)
		return
	}
	edgeAuto.update(st)
}

// hostOfBaseURL 从渠道 Base URL 里取主机名（失败返回空）。
func hostOfBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
