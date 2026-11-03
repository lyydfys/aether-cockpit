package gateway

import (
	"testing"
	"time"
)

// 计数器必须拒绝"投影超过上限"的请求；这正是并发突发越限的防线。
func TestQuotaReserveBlocksProjectedOverflow(t *testing.T) {
	q := newQuotaTracker()
	fixed := time.Date(2026, 11, 4, 10, 0, 0, 0, time.Local)
	q.now = func() time.Time { return fixed }
	limit := &callerKeyLimit{Period: "day", Tokens: 1000}

	// 第一次预留 600：放行。
	if _, ok := q.tryReserve("k", limit, 600, 0); !ok {
		t.Fatal("首笔预留应放行")
	}
	// 第二次再要 600：600+600 > 1000，必须拒（即使账本里一条记录都没有）。
	if _, ok := q.tryReserve("k", limit, 600, 0); ok {
		t.Fatal("在途预留叠加后超过上限，应拒绝")
	}
	if n := q.rejectedCount("k"); n != 1 {
		t.Fatalf("被拒次数应为 1，得到 %d", n)
	}
}

// release 要归还预留，否则一次失败请求会永久吃掉额度。
func TestQuotaReleaseReturnsReservation(t *testing.T) {
	q := newQuotaTracker()
	limit := &callerKeyLimit{Period: "day", Tokens: 1000}
	res, ok := q.tryReserve("k", limit, 900, 0)
	if !ok {
		t.Fatal("首笔预留应放行")
	}
	q.release(res)
	if _, ok := q.tryReserve("k", limit, 900, 0); !ok {
		t.Fatal("归还后应能再次预留")
	}
}

// settle 用实际值替换预留，且只能生效一次（release 兜底不能重复扣）。
func TestQuotaSettleReplacesReservationOnce(t *testing.T) {
	q := newQuotaTracker()
	limit := &callerKeyLimit{Period: "day", Tokens: 1000}
	res, ok := q.tryReserve("k", limit, 800, 0)
	if !ok {
		t.Fatal("预留应放行")
	}
	q.settle(res, 100, 0)
	q.release(res) // 成功路径上的兜底 release 必须变成空操作
	tokens, _ := q.spent("k", limit)
	if tokens != 100 {
		t.Fatalf("结算后已用应为实际值 100，得到 %v", tokens)
	}
}

// 日历窗口：跨过零点整段归零，不带上一天的用量。
func TestQuotaCalendarWindowResets(t *testing.T) {
	q := newQuotaTracker()
	day1 := time.Date(2026, 11, 4, 23, 59, 0, 0, time.Local)
	q.now = func() time.Time { return day1 }
	limit := &callerKeyLimit{Period: "day", Tokens: 1000}
	q.add("k", 900, 0)
	if tokens, _ := q.spent("k", limit); tokens != 900 {
		t.Fatalf("当天应为 900，得到 %v", tokens)
	}

	day2 := time.Date(2026, 11, 5, 0, 1, 0, 0, time.Local)
	q.now = func() time.Time { return day2 }
	if tokens, _ := q.spent("k", limit); tokens != 0 {
		t.Fatalf("跨天后应归零，得到 %v", tokens)
	}
}

// 周窗口从周一开始：周日属于上一周。
func TestCalendarWeekStartsMonday(t *testing.T) {
	wed := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local)
	mon := time.Date(2026, 11, 2, 0, 0, 0, 0, time.Local)
	if got, _ := calendarWindowStart("week", wed); got != mon.Unix() {
		t.Fatalf("周三的周窗口起点应为周一：%v vs %v", time.Unix(got, 0), mon)
	}
}

// 轮转窗口按小时桶累计，且不会算进窗口外的旧数据。
func TestQuotaRollingHoursWindow(t *testing.T) {
	q := newQuotaTracker()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local)
	q.now = func() time.Time { return now }
	limit := &callerKeyLimit{Period: "hours", Hours: 2, Tokens: 1000}

	q.add("k", 500, 0)
	q.now = func() time.Time { return now.Add(3 * time.Hour) }
	if tokens, _ := q.spent("k", limit); tokens != 0 {
		t.Fatalf("3 小时前的用量不该算进 2 小时窗口，得到 %v", tokens)
	}
}

// 重启后要能用账本历史种一次，否则硬上限在重启后立刻失效。
func TestQuotaSeedFromLedger(t *testing.T) {
	q := newQuotaTracker()
	now := time.Now()
	q.now = func() time.Time { return now }
	recs := []ledgerRecord{
		{T: now.UnixMilli(), CallerKey: "k", In: 400, Out: 100, Status: 200},
		{T: now.UnixMilli(), CallerKey: "other", In: 9999, Status: 200},
	}
	q.seed("k", recs)
	limit := &callerKeyLimit{Period: "day", Tokens: 1000}
	if tokens, _ := q.spent("k", limit); tokens != 500 {
		t.Fatalf("种子应只累加本 key 的 500，得到 %v", tokens)
	}
	// 重复种不能重复累加。
	q.seed("k", recs)
	if tokens, _ := q.spent("k", limit); tokens != 500 {
		t.Fatalf("重复种子不能翻倍，得到 %v", tokens)
	}
}

// 费用只从账本侧累加（addCost），并且要能触发费用上限。
func TestQuotaCostLimit(t *testing.T) {
	q := newQuotaTracker()
	limit := &callerKeyLimit{Period: "day", Cost: 1.0}
	q.addCost("k", 0.8)
	if _, ok := q.tryReserve("k", limit, 10, 0.5); ok {
		t.Fatal("0.8 + 0.5 超过 1.0，应拒绝")
	}
	if _, ok := q.tryReserve("k", limit, 10, 0.1); !ok {
		t.Fatal("0.8 + 0.1 未超上限，应放行")
	}
}
