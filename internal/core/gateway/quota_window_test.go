package gateway

import (
	"testing"
	"time"
)

// 多窗口并行：任意一个窗口触顶就拒，且各窗口独立计时。
//
// 场景（对齐 aether 的并行预算）：一个 key 同时挂「每小时 1000 token」和
// 「每天 50000 token」。小时窗口满时立即拒；跨过一个整点后小时窗口归零，
// 但日窗口仍在累计。
func TestQuotaMultipleWindowsAllEnforced(t *testing.T) {
	q := newQuotaTracker()
	base := time.Date(2026, 11, 4, 10, 0, 0, 0, time.Local)
	q.now = func() time.Time { return base }
	limit := &callerKeyLimit{
		Windows: []callerKeyWindow{
			{Period: "hours", Hours: 1, Tokens: 1000},
			{Period: "day", Tokens: 50000},
		},
	}

	// 小时窗口内：800 + 300 超过 1000 → 拒。
	if _, ok := q.tryReserve("k", limit, 800, 0); !ok {
		t.Fatal("首笔应放行")
	}
	if _, ok := q.tryReserve("k", limit, 300, 0); ok {
		t.Fatal("小时窗口应触顶拒绝")
	}
	q.release(nil) // no-op

	// 结算 800 后，跨到下一小时：小时窗口归零，又能放行。
	res := &reservation{tracker: q, keyID: "k", tokens: 800}
	q.settle(res, 800, 0)
	q.now = func() time.Time { return base.Add(61 * time.Minute) }
	if _, ok := q.tryReserve("k", limit, 800, 0); !ok {
		t.Fatal("跨小时后小时窗口应重新可放行")
	}
}

// 日窗口先触顶时，即使小时窗口还空也要拒。
func TestQuotaDayWindowBlocksBeforeHour(t *testing.T) {
	q := newQuotaTracker()
	q.now = func() time.Time { return time.Date(2026, 11, 4, 10, 0, 0, 0, time.Local) }
	limit := &callerKeyLimit{
		Windows: []callerKeyWindow{
			{Period: "hours", Hours: 1, Tokens: 100000},
			{Period: "day", Tokens: 1000},
		},
	}
	q.add("k", 1200, 0) // 已用超过日窗口
	if _, ok := q.tryReserve("k", limit, 10, 0); ok {
		t.Fatal("日窗口已超，必须拒绝")
	}
}

// 老的单窗口配置行为不变（没配 Windows 时仍按单窗口判）。
func TestQuotaLegacySingleWindowStillWorks(t *testing.T) {
	q := newQuotaTracker()
	limit := &callerKeyLimit{Period: "days", Days: 1, Tokens: 100}
	if _, ok := q.tryReserve("k", limit, 50, 0); !ok {
		t.Fatal("单窗口配置应可用")
	}
	if _, ok := q.tryReserve("k", limit, 60, 0); ok {
		t.Fatal("单窗口配置仍要能拒绝")
	}
}

// 没设任何窗口时不预留、不拒绝。
func TestQuotaNoWindowAllowsEverything(t *testing.T) {
	q := newQuotaTracker()
	if _, ok := q.tryReserve("k", &callerKeyLimit{RPM: 10}, 1e9, 1e9); !ok {
		t.Fatal("没设 token/费用窗口时不该拦")
	}
	if _, ok := q.tryReserve("k", nil, 1e9, 1e9); !ok {
		t.Fatal("limit 为 nil 时不该拦")
	}
}
