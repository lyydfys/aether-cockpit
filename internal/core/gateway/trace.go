package gateway

import (
	"context"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 逐请求路由轨迹 —— magpie 路由页的数据源。
//
// 页面按 `routes[]` 实时播放每个请求的决策：候选顺序（order）、
// 每个尝试（tries）落到哪个账号、HTTP 状态、耗时、失败原因与最终命中。
// 这里只记录真实发生过的事实；界面上的解释文案由前端按 status/fail 生成。

const traceMaxRoutes = 500

// traceStore 是进程内的环形轨迹缓冲（足够页面回看当天最近几百个请求）。
type traceStore struct {
	mu     sync.Mutex
	seq    int64
	routes []*traceRoute
	// waiters 收到新轨迹时被唤醒（长轮询用）。
	waiters []chan struct{}
}

type traceTry struct {
	ID     string `json:"id"`
	Model  string `json:"model,omitempty"`
	Start  int64  `json:"start"` // ms
	Ms     int64  `json:"ms"`
	Status int    `json:"status"`
	Done   bool   `json:"done"`
	Fail   string `json:"fail,omitempty"`
	Picked bool   `json:"picked,omitempty"`
	// Upstream 是实际命中的上游模型名（与请求模型不同时页面会标注）。
	Upstream string `json:"upstream,omitempty"`
}

type traceSeat struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // account | provider
	Provider string `json:"provider,omitempty"`
	Name     string `json:"name,omitempty"`
	Who      string `json:"who,omitempty"`
	Model    string `json:"model,omitempty"`
}

type traceRoute struct {
	ID      int64       `json:"id"`
	Agent   string      `json:"agent"`
	Model   string      `json:"model"`
	Time    int64       `json:"time"` // ms
	Ms      int64       `json:"ms"`
	Status  int         `json:"status"`
	Done    bool        `json:"done"`
	Session string      `json:"session,omitempty"`
	Tries   []traceTry  `json:"tries"`
	Order   []traceSeat `json:"order"`
	Left    []traceSeat `json:"left"`

	started time.Time
	store   *traceStore
}

func newTraceStore() *traceStore { return &traceStore{} }

// traceAgent 把 UA 识别结果映射成 magpie 路由页认识的 agent id。
func traceAgent(client string) string {
	switch client {
	case "claude-code":
		return "claude"
	case "gemini-cli":
		return "gemini"
	}
	return client
}

func (s *traceStore) begin(agent, model, session string) *traceRoute {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	r := &traceRoute{
		ID:      s.seq,
		Agent:   traceAgent(agent),
		Model:   model,
		Time:    time.Now().UnixMilli(),
		Done:    false,
		Session: session,
		Tries:   []traceTry{},
		Order:   []traceSeat{},
		Left:    []traceSeat{},
		started: time.Now(),
		store:   s,
	}
	s.routes = append(s.routes, r)
	if len(s.routes) > traceMaxRoutes {
		s.routes = s.routes[len(s.routes)-traceMaxRoutes:]
	}
	return r
}

// addSeat 记录一个候选（同一账号+模型只记一次）。
func (t *traceRoute) addSeat(seat traceSeat) {
	s := t.store
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range t.Order {
		if x.ID == seat.ID && x.Model == seat.Model {
			return
		}
	}
	t.Order = append(t.Order, seat)
}

// addTry 记录一次真实的上游尝试，并在成功时标记为最终命中。
func (t *traceRoute) addTry(env *provider.AccountEnv, upstream string, start time.Time, status int, fail string, picked bool) {
	s := t.store
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Tries = append(t.Tries, traceTry{
		ID:       env.AccountID,
		Model:    upstream,
		Start:    start.UnixMilli(),
		Ms:       time.Since(start).Milliseconds(),
		Status:   status,
		Done:     true,
		Fail:     fail,
		Picked:   picked,
		Upstream: upstream,
	})
	t.rebuildLeftLocked(s)
	if picked {
		t.Status = status
	}
	t.wakeLocked(s)
}

// finish 在请求结束时落定轨迹（含中途失败、客户端取消）。
func (t *traceRoute) finish() {
	s := t.store
	s.mu.Lock()
	defer s.mu.Unlock()
	// 没被任何尝试标记成功时，请求状态取最后一次尝试；一次都没试
	// （没有可用候选）按 499 记，页面据此显示为失败而不是 undefined。
	if t.Status == 0 {
		if len(t.Tries) > 0 {
			t.Status = t.Tries[len(t.Tries)-1].Status
		} else {
			t.Status = 499
		}
	}
	if !t.Done {
		t.Done = true
		t.Ms = time.Since(t.started).Milliseconds()
	}
	t.rebuildLeftLocked(s)
	t.wakeLocked(s)
}

func (t *traceRoute) rebuildLeftLocked(s *traceStore) {
	tried := make(map[string]bool, len(t.Tries))
	for _, tr := range t.Tries {
		tried[tr.ID+"\x00"+tr.Model] = true
	}
	left := make([]traceSeat, 0, len(t.Order))
	for _, seat := range t.Order {
		if !tried[seat.ID+"\x00"+seat.Model] {
			left = append(left, seat)
		}
	}
	t.Left = left
}

func (t *traceRoute) wakeLocked(s *traceStore) {
	for _, ch := range s.waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// waitAfter 等到出现 id > after 的轨迹，或 ctx/上限到期。
func (s *traceStore) waitAfter(ctx context.Context, after int64, max time.Duration) {
	deadline := time.NewTimer(max)
	defer deadline.Stop()
	for {
		s.mu.Lock()
		if s.seq > after {
			s.mu.Unlock()
			return
		}
		ch := make(chan struct{}, 1)
		s.waiters = append(s.waiters, ch)
		s.mu.Unlock()

		select {
		case <-ctx.Done():
			s.removeWaiter(ch)
			return
		case <-deadline.C:
			s.removeWaiter(ch)
			return
		case <-ch:
			s.removeWaiter(ch)
		}
	}
}

func (s *traceStore) removeWaiter(ch chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.waiters {
		if x == ch {
			s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
			return
		}
	}
}

// since 返回 id > after 的轨迹（快照，调用方不会再改）。
func (s *traceStore) since(after int64) []traceRoute {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]traceRoute, 0, 8)
	for _, r := range s.routes {
		if r.ID > after {
			out = append(out, *r)
		}
	}
	return out
}

// totals 返回页面顶部的三个计数。
func (s *traceStore) totals() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var req, reroute, errs int64
	for _, r := range s.routes {
		req++
		if len(r.Tries) > 1 {
			reroute++
		}
		last := len(r.Tries) - 1
		if last >= 0 && r.Tries[last].Status >= 400 {
			errs++
		}
	}
	return map[string]int64{"requests": req, "rerouted": reroute, "errors": errs}
}

// byDay 取某天（本地时区）的轨迹，供路由页的历史日期条。
func (s *traceStore) byDay(day string) ([]traceRoute, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]traceRoute, 0, 32)
	cut := len(s.routes) >= traceMaxRoutes
	for _, r := range s.routes {
		if time.UnixMilli(r.Time).Format("2006-01-02") == day {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, cut
}

// full 报告缓冲是否已经因为容量淘汰过旧轨迹。
func (s *traceStore) full() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.routes) >= traceMaxRoutes
}

func (s *traceStore) get(id int64) *traceRoute {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.routes {
		if r.ID == id {
			cp := *r
			return &cp
		}
	}
	return nil
}

func (s *traceStore) dayCounts() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := make(map[string]int64, 16)
	for _, r := range s.routes {
		counts[time.UnixMilli(r.Time).Format("2006-01-02")]++
	}
	days := make([]string, 0, len(counts))
	for d := range counts {
		days = append(days, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	out := make([]map[string]any, 0, len(days))
	for _, d := range days {
		out = append(out, map[string]any{"day": d, "requests": counts[d]})
	}
	return out
}

/* ---------------- HTTP ---------------- */

// handleGatewayTrace GET /api/gateway/trace?after=N[&wait=1]
func (g *Gateway) handleGatewayTrace(w http.ResponseWriter, r *http.Request) {
	if g.traces == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"seq": 0, "now": time.Now().UnixMilli(), "mine": true, "lifted": map[string]any{},
			"routes": []any{}, "totals": map[string]int64{"requests": 0, "rerouted": 0, "errors": 0},
			"by": map[string]any{"pid": os.Getpid()},
		})
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if r.URL.Query().Get("wait") == "1" {
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		g.traces.waitAfter(ctx, after, 24*time.Second)
		cancel()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"seq":    g.traces.snapshotSeq(),
		"now":    time.Now().UnixMilli(),
		"mine":   true,
		"lifted": map[string]any{},
		"routes": g.traces.since(after),
		"totals": g.traces.totals(),
		"by":     map[string]any{"pid": os.Getpid(), "serves": "Aether Cockpit"},
	})
}

func (s *traceStore) snapshotSeq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

// handleGatewayHistory GET /api/gateway/history?day=YYYY-MM-DD
func (g *Gateway) handleGatewayHistory(w http.ResponseWriter, r *http.Request) {
	if g.traces == nil {
		writeJSON(w, http.StatusOK, map[string]any{"days": []any{}, "routes": []any{}, "cut": false})
		return
	}
	day := r.URL.Query().Get("day")
	days := g.traces.dayCounts()
	routes := []traceRoute{}
	cut := false
	if day != "" {
		routes, cut = g.traces.byDay(day)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"days":   days,
		"routes": routes,
		"cut":    cut,
	})
}

// handleGatewayRoute GET /api/gateway/route?id=N —— 点开历史请求时按 id 取回。
func (g *Gateway) handleGatewayRoute(w http.ResponseWriter, r *http.Request) {
	if g.traces == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "请求轨迹已不在内存中"})
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	route := g.traces.get(id)
	if route == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "请求轨迹已不在内存中"})
		return
	}
	writeJSON(w, http.StatusOK, route)
}

// traceFailCode 把内核错误码映射成页面认识的失败词。
func traceFailCode(e *ir.APIError) (int, string) {
	if e == nil {
		return 200, ""
	}
	switch e.Code {
	case ir.ErrCodeUpstreamRateLimit:
		return http.StatusTooManyRequests, "rate"
	case ir.ErrCodeUpstreamAuth:
		return http.StatusUnauthorized, "auth"
	case ir.ErrCodeBadRequest:
		return http.StatusBadRequest, "shape"
	case ir.ErrCodeNoAccount, ir.ErrCodeNoProvider:
		return http.StatusServiceUnavailable, "other"
	default:
		return http.StatusBadGateway, "other"
	}
}
