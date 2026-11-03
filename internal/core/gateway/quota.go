package gateway

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 网关密钥的用量计费（限额判定）。
//
// 为什么要单独一套内存计数，而不是每次请求去翻账本：
//  1. 热路径成本：原实现每个带 key 的请求都调 ledger.records(from,to)，
//     持锁把 30 天每天从磁盘 loadDay 进来再排序——一次请求的代价随历史
//     长度线性增长。
//  2. 正确性：账本单日上限 5000 条（ledgerMaxDay），高流量日会丢最旧的
//     记录，于是"已用"偏小、硬上限被穿透。计数器只增不减，不会被截断。
//  3. 并发：原实现只判"已用 ≥ 上限"，100 个并发请求会同时通过、一起烧掉
//     上限的 100 倍。这里加"在途预留"（reserve/settle），先占额度再发请求。
//
// 窗口语义对齐 aether：day / week / month 是**日历窗口**（到点整段归零），
// days / hours 是**滚动窗口**（按小时桶累计）。滚动窗口用小时桶而不是逐条
// 事件，是为了让内存有界（每个 key 最多 31×24 个桶）。

const quotaBucketMaxAge = 31 * 24 * time.Hour

// quotaUsage 是一个 key 的累计用量。
type quotaUsage struct {
	// buckets 是滚动窗口用的小时桶：hourStart(unix) -> tokens/cost。
	buckets map[int64]*quotaBucket
	// calendar 是日历窗口的计数：period -> windowStart(unix) -> tokens/cost。
	// 换窗口时整条重置，因此每个 period 只有一个条目。
	calendar map[string]*quotaBucket
	// inflight 是在途预留（尚未回填实际值的请求）。
	inflightTokens float64
	inflightCost   float64
	// rejected 是"因超限被拒"的次数（累计，不清零）。
	rejected int64
	// seeded 表示已经用账本历史种过一次（避免重启后清零）。
	seeded bool
}

type quotaBucket struct {
	start  int64
	tokens float64
	cost   float64
}

// quotaTracker 是所有 key 的计数器集合。
type quotaTracker struct {
	mu   sync.Mutex
	byID map[string]*quotaUsage
	// now 可注入，便于测试窗口边界。
	now func() time.Time
}

func newQuotaTracker() *quotaTracker {
	return &quotaTracker{byID: map[string]*quotaUsage{}, now: time.Now}
}

// keyQuotaTracker 返回计数器，必要时惰性创建。
//
// 惰性创建是为了兜住"直接用 &Gateway{...} 构造"的路径（测试、移动端运行
// 时注入）：这些路径不会走 New()，如果限额判定因为 nil 而静默放行，
// 硬上限就等于不存在。
func (g *Gateway) keyQuotaTracker() *quotaTracker {
	if g.keyQuota == nil {
		g.keyQuotaOnce.Do(func() {
			if g.keyQuota == nil {
				g.keyQuota = newQuotaTracker()
			}
		})
	}
	return g.keyQuota
}

func (q *quotaTracker) usageLocked(id string) *quotaUsage {
	u := q.byID[id]
	if u == nil {
		u = &quotaUsage{buckets: map[int64]*quotaBucket{}, calendar: map[string]*quotaBucket{}}
		q.byID[id] = u
	}
	return u
}

// calendarWindowStart 返回 period 对应的日历窗口起点。
//
// 周从周一开始（ISO）：aether 与多数订阅账单都以周一为界。
func calendarWindowStart(period string, now time.Time) (int64, bool) {
	switch period {
	case "day":
		t := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		return t.Unix(), true
	case "week":
		t := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		// Go 的 Weekday 里周日是 0：往回退 (weekday+6)%7 天即周一。
		offset := (int(t.Weekday()) + 6) % 7
		return t.AddDate(0, 0, -offset).Unix(), true
	case "month":
		t := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return t.Unix(), true
	}
	return 0, false
}

// rollingSince 把 days / hours 窗口换成"起点时间"。
func rollingSince(limit *callerKeyLimit, now time.Time) (time.Time, bool) {
	if limit == nil {
		return time.Time{}, false
	}
	switch limit.Period {
	case "hours":
		if limit.Hours > 0 {
			return now.Add(-time.Duration(limit.Hours) * time.Hour), true
		}
	case "days":
		if limit.Days > 0 {
			return now.AddDate(0, 0, -limit.Days), true
		}
	}
	return time.Time{}, false
}

// quotaWindow 是"一个限额窗口"的统一形态。
//
// 为什么要抽象出来：aether 的预算模型允许一个 key 同时挂多个窗口
// （小时 / 日 / 周 / 月各自独立），只要**任意一个**窗口超了就拒。
// 老的 callerKeyLimit 只有一组 Period/Tokens/Cost，把它当成"一个窗口"，
// 与显式配置的 Windows 走同一条判定路径。
type quotaWindow struct {
	Period string
	Days   int
	Hours  int
	Tokens int64
	Cost   float64
}

// limitWindows 把 callerKeyLimit 展开成窗口列表。
//
// 兼容规则：没配 Windows 时，把老的单窗口字段当作唯一窗口——这样升级前
// 保存的配置行为完全不变。
func limitWindows(limit *callerKeyLimit) []quotaWindow {
	if limit == nil {
		return nil
	}
	out := make([]quotaWindow, 0, len(limit.Windows)+1)
	for _, w := range limit.Windows {
		if w.Tokens <= 0 && w.Cost <= 0 {
			continue // 空窗口等于没设，忽略
		}
		out = append(out, quotaWindow{
			Period: strings.ToLower(strings.TrimSpace(w.Period)),
			Days:   w.Days, Hours: w.Hours, Tokens: w.Tokens, Cost: w.Cost,
		})
	}
	if limit.Tokens > 0 || limit.Cost > 0 {
		out = append(out, quotaWindow{
			Period: strings.ToLower(strings.TrimSpace(limit.Period)),
			Days:   limit.Days, Hours: limit.Hours,
			Tokens: limit.Tokens, Cost: limit.Cost,
		})
	}
	return out
}

// windowKey 是计数器里区分窗口的键（日历窗口按 period，滚动窗口按跨度）。
func windowKey(w quotaWindow) string {
	if _, ok := calendarWindowStart(w.Period, time.Now()); ok {
		return "cal:" + w.Period
	}
	return fmt.Sprintf("roll:%s:%d:%d", w.Period, w.Days, w.Hours)
}

// usedIn 返回该窗口内已用的 tokens / cost（不含在途预留；调用方持锁）。
func (q *quotaTracker) usedInLocked(u *quotaUsage, w quotaWindow, now time.Time) (float64, float64) {
	if start, ok := calendarWindowStart(w.Period, now); ok {
		if b := u.calendar[w.Period]; b != nil && b.start == start {
			return b.tokens, b.cost
		}
		return 0, 0
	}
	limit := &callerKeyLimit{Period: w.Period, Days: w.Days, Hours: w.Hours}
	since, ok := rollingSince(limit, now)
	if !ok {
		return 0, 0
	}
	cut := since.Unix()
	var tokens, cost float64
	for start, b := range u.buckets {
		// 桶按小时对齐：起点不早于 since 的整桶计入，最后一桶可能少算
		// 最多一小时——用固定桶换有界内存的已知代价。
		if start >= cut {
			tokens += b.tokens
			cost += b.cost
		}
	}
	return tokens, cost
}

// spent 返回该 key 在"最紧的窗口"里已用的 tokens / cost（含在途预留）。
//
// 为什么给最紧的而不是求和：调用方（页面、诊断）关心"离触顶还有多远"，
// 多窗口下答案是"最接近上限的那个窗口"。
func (q *quotaTracker) spent(id string, limit *callerKeyLimit) (tokens float64, cost float64) {
	windows := limitWindows(limit)
	if len(windows) == 0 {
		return 0, 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	u := q.usageLocked(id)
	now := q.now()

	bestRatio := -1.0
	for _, w := range windows {
		t, c := q.usedInLocked(u, w, now)
		t += u.inflightTokens
		c += u.inflightCost
		r := math.Max(ratio(t, float64(w.Tokens)), ratio(c, w.Cost))
		if r > bestRatio {
			bestRatio, tokens, cost = r, t, c
		}
	}
	return tokens, cost
}

// ratio 返回 v/limit（limit<=0 时视为 0）。
func ratio(v, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return v / limit
}

// reservation 是一次请求的额度预留。settle 与 release 只生效一次。
type reservation struct {
	tracker *quotaTracker
	keyID   string
	tokens  float64
	cost    float64
	done    atomic.Bool
}

// tryReserve 尝试为一次请求预留额度。超限返回 (nil, false)。
//
// 多窗口语义：**每个窗口都必须通过**才算放行（任意一个窗口触顶就拒）。
// 这就是 aether 的"5 个并行窗口"——小时、日、周、月各自独立计时。
func (q *quotaTracker) tryReserve(id string, limit *callerKeyLimit, tokens, cost float64) (*reservation, bool) {
	windows := limitWindows(limit)
	if len(windows) == 0 {
		return nil, true // 没设限额：不必预留
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	u := q.usageLocked(id)
	now := q.now()

	for _, w := range windows {
		usedTokens, usedCost := q.usedInLocked(u, w, now)
		projectedTokens := usedTokens + u.inflightTokens + tokens
		projectedCost := usedCost + u.inflightCost + cost
		// 只在"本次预留会把额度顶过去"时拒绝；已用超了也必须拒绝。
		if w.Tokens > 0 && projectedTokens > float64(w.Tokens) {
			u.rejected++
			return nil, false
		}
		if w.Cost > 0 && projectedCost > w.Cost {
			u.rejected++
			return nil, false
		}
	}
	u.inflightTokens += tokens
	u.inflightCost += cost
	return &reservation{tracker: q, keyID: id, tokens: tokens, cost: cost}, true
}

// release 归还预留（请求失败/被取消时调用）。
func (q *quotaTracker) release(r *reservation) {
	if r == nil || !r.done.CompareAndSwap(false, true) {
		return
	}
	q.mu.Lock()
	u := q.usageLocked(r.keyID)
	u.inflightTokens -= r.tokens
	u.inflightCost -= r.cost
	q.mu.Unlock()
}

// settle 用实际值替换预留（请求成功、拿到真实 usage 后调用）。
//
// 实际值可能远小于预留（上游返回很少），也可能更大（长输出）：这里直接
// 用实际值记账并把预留还掉，两边都不留偏差。
func (q *quotaTracker) settle(r *reservation, tokens int64, cost float64) {
	if r == nil || !r.done.CompareAndSwap(false, true) {
		return
	}
	q.mu.Lock()
	u := q.usageLocked(r.keyID)
	u.inflightTokens -= r.tokens
	u.inflightCost -= r.cost
	q.addLocked(u, float64(tokens), cost, q.now())
	q.mu.Unlock()
}

// add 把一次已完成的用量记进计数器（没有预留时用，例如本机请求）。
func (q *quotaTracker) add(id string, tokens int64, cost float64) {
	if id == "" || (tokens == 0 && cost == 0) {
		return
	}
	q.mu.Lock()
	u := q.usageLocked(id)
	q.addLocked(u, float64(tokens), cost, q.now())
	q.mu.Unlock()
}

// addCost 只累加费用（账本算出权威成本后调用）。
func (q *quotaTracker) addCost(id string, cost float64) {
	if id == "" || cost <= 0 {
		return
	}
	q.mu.Lock()
	u := q.usageLocked(id)
	q.addLocked(u, 0, cost, q.now())
	q.mu.Unlock()
}

// addLocked 同时更新小时桶与日历窗口（调用方持锁）。
func (q *quotaTracker) addLocked(u *quotaUsage, tokens, cost float64, now time.Time) {
	if tokens == 0 && cost == 0 {
		return
	}
	hour := now.Truncate(time.Hour).Unix()
	b := u.buckets[hour]
	if b == nil {
		b = &quotaBucket{start: hour}
		u.buckets[hour] = b
	}
	b.tokens += tokens
	b.cost += cost
	// 顺手清掉过期桶：只在有新数据时做，摊平到每次请求上代价可忽略。
	for start := range u.buckets {
		if now.Sub(time.Unix(start, 0)) > quotaBucketMaxAge {
			delete(u.buckets, start)
		}
	}

	if _, ok := calendarWindowStart("day", now); ok {
		for _, period := range []string{"day", "week", "month"} {
			start, _ := calendarWindowStart(period, now)
			cb := u.calendar[period]
			if cb == nil || cb.start != start {
				cb = &quotaBucket{start: start}
				u.calendar[period] = cb
			}
			cb.tokens += tokens
			cb.cost += cost
		}
	}
}

// rejectedCount 返回因超限被拒的累计次数。
func (q *quotaTracker) rejectedCount(id string) int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	if u := q.byID[id]; u != nil {
		return u.rejected
	}
	return 0
}

// noteRejected 记一次超限拒绝（累计，不随窗口清零）。
func (q *quotaTracker) noteRejected(id string) {
	if id == "" {
		return
	}
	q.mu.Lock()
	q.usageLocked(id).rejected++
	q.mu.Unlock()
}

// reservationKey 是请求上下文里存放预留的键。
type reservationKeyType struct{}

var reservationKey reservationKeyType

// withReservation 把本次请求的额度预留放进上下文，供用量结算时取用。
func withReservation(ctx context.Context, r *reservation) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, reservationKey, r)
}

// reservationFrom 取出上下文里的预留（没有则为 nil）。
func reservationFrom(ctx context.Context) *reservation {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(reservationKey).(*reservation)
	return r
}

// noteRejectedRequest 记录一次"因超限被拒"的请求。
//
// 目的很具体：被拒的请求原来既不写账本也不计数，于是"今天被限流拒了多少次"
// 查不到。现在两边都记——计数器给面板，账本给审计（谁、什么时候、什么模型）。
func (g *Gateway) noteRejectedRequest(keyID, reason string, r *http.Request) {
	if g.keyQuota != nil {
		g.keyQuota.noteRejected(keyID)
	}
	if g.ledger == nil {
		return
	}
	model := ""
	if r != nil {
		model = r.Header.Get("X-Aether-Model")
	}
	g.ledger.add(ledgerRecord{
		T:         time.Now().UnixMilli(),
		Agent:     clientAgent(r),
		Model:     model,
		CallerKey: keyID,
		Status:    http.StatusTooManyRequests,
		ErrType:   "caller_key_limited",
		Access:    reason,
	})
}

// seed 用账本里的历史填一次计数器（进程启动后第一次判限额时调用）。
//
// 没有这一步，重启会让"今天已用"清零，硬上限在重启后立刻失效。
func (q *quotaTracker) seed(id string, records []ledgerRecord) {
	q.mu.Lock()
	defer q.mu.Unlock()
	u := q.usageLocked(id)
	if u.seeded {
		return
	}
	u.seeded = true
	for _, r := range records {
		if r.CallerKey != id {
			continue
		}
		tokens := float64(r.In + r.Out + r.CacheRead + r.CacheWrite)
		q.addLocked(u, tokens, r.Cost, time.UnixMilli(r.T))
	}
}
