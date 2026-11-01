package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) *Schedule {
	t.Helper()
	s, err := ParseCron(expr)
	if err != nil {
		t.Fatalf("解析 %q 失败: %v", expr, err)
	}
	return s
}

func TestParseCronValid(t *testing.T) {
	valid := []string{
		"* * * * *",
		"0 9 * * *",
		"30 8 * * 1-5",
		"*/15 * * * *",
		"0 0,12 * * *",
		"0 9 1-7 * *",
		"5/10 * * * *",
	}
	for _, e := range valid {
		if _, err := ParseCron(e); err != nil {
			t.Errorf("%q 应当合法: %v", e, err)
		}
	}
}

func TestParseCronInvalid(t *testing.T) {
	invalid := []string{
		"",
		"* * * *",     // 少一个字段
		"* * * * * *", // 多一个字段
		"60 * * * *",  // 分钟越界
		"* 24 * * *",  // 小时越界
		"* * 0 * *",   // 日期越界
		"* * * 13 *",  // 月份越界
		"* * * * 7",   // 星期越界
		"*/0 * * * *", // 步长为 0
		"a * * * *",   // 非数字
	}
	for _, e := range invalid {
		if _, err := ParseCron(e); err == nil {
			t.Errorf("%q 应当报错", e)
		}
	}
}

func TestNextDailyAt9(t *testing.T) {
	s := mustParse(t, "0 9 * * *")
	// 从 2026-10-26 08:00 出发，下一个应是当天 09:00
	base := time.Date(2026, 10, 26, 8, 0, 0, 0, time.Local)
	got := s.Next(base)
	want := time.Date(2026, 10, 26, 9, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("Next = %v, want %v", got, want)
	}

	// 从 09:00 出发（正好在点上），应跳到次日 09:00
	got = s.Next(want)
	want2 := time.Date(2026, 10, 27, 9, 0, 0, 0, time.Local)
	if !got.Equal(want2) {
		t.Errorf("Next(准点) = %v, want %v", got, want2)
	}
}

func TestNextEvery15Minutes(t *testing.T) {
	s := mustParse(t, "*/15 * * * *")
	base := time.Date(2026, 10, 26, 8, 3, 0, 0, time.Local)
	got := s.Next(base)
	want := time.Date(2026, 10, 26, 8, 15, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("Next = %v, want %v", got, want)
	}
}

func TestNextWeekdayOnly(t *testing.T) {
	// 2026-10-31 是周六，下一个工作日应是 11-02 周一
	s := mustParse(t, "30 8 * * 1-5")
	base := time.Date(2026, 10, 31, 10, 0, 0, 0, time.Local)
	got := s.Next(base)
	want := time.Date(2026, 11, 2, 8, 30, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("Next = %v, want %v", got, want)
	}
}

func TestNextMonthRollover(t *testing.T) {
	s := mustParse(t, "0 0 1 * *") // 每月 1 日 0 点
	base := time.Date(2026, 10, 15, 12, 0, 0, 0, time.Local)
	got := s.Next(base)
	want := time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("Next = %v, want %v", got, want)
	}
}

func TestNextNeverMatches(t *testing.T) {
	// 2 月 30 日永远不会到来，Next 应返回零值而不是死循环。
	s := mustParse(t, "0 0 30 2 *")
	got := s.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local))
	if !got.IsZero() {
		t.Errorf("不可能满足的表达式应返回零值，实际 %v", got)
	}
}

func TestJitterIsStable(t *testing.T) {
	base := time.Date(2026, 10, 26, 9, 0, 0, 0, time.Local)
	a := Jitter(base, 15*time.Minute, "mock|daily")
	b := Jitter(base, 15*time.Minute, "mock|daily")
	if !a.Equal(b) {
		t.Errorf("同一 seed 应产生相同偏移: %v vs %v", a, b)
	}
	if a.Before(base) {
		t.Errorf("偏移不应早于基准: %v", a)
	}
	if a.Sub(base) > 15*time.Minute {
		t.Errorf("偏移超界: %v", a.Sub(base))
	}
}

func TestJitterDiffersBySeed(t *testing.T) {
	base := time.Date(2026, 10, 26, 9, 0, 0, 0, time.Local)
	seen := map[time.Time]bool{}
	for _, seed := range []string{"p|a", "p|b", "p|c", "p|d", "p|e"} {
		seen[Jitter(base, 15*time.Minute, seed)] = true
	}
	if len(seen) < 3 {
		t.Errorf("不同 seed 的偏移过于集中，只产生 %d 个不同值", len(seen))
	}
}

func TestStateRecordAndQuery(t *testing.T) {
	st := NewState("") // 纯内存
	day := DayKey(time.Now())

	if st.IsDone("acc1", "task1", day) {
		t.Fatal("初始状态不应为已完成")
	}

	st.Record("acc1", "task1", Result{Success: true, Points: 100}, day)
	if !st.IsDone("acc1", "task1", day) {
		t.Error("记录后应为已完成")
	}

	// 失败不应覆盖已有的成功记录
	st.Record("acc1", "task1", Result{Success: false, Message: "retry failed"}, day)
	if !st.IsDone("acc1", "task1", day) {
		t.Error("失败记录不应冲掉成功状态")
	}

	if st.CompletedToday() != 1 {
		t.Errorf("CompletedToday = %d, want 1", st.CompletedToday())
	}

	st.Reset("acc1", "task1")
	if st.IsDone("acc1", "task1", day) {
		t.Error("Reset 后不应为已完成")
	}
}

func TestQueueSerialOrder(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	q := newProviderQueue(context.Background(), "p", func(string, ...any) {},
		func(ctx context.Context, pid string, j queuedJob) {
			mu.Lock()
			order = append(order, j.accountID)
			mu.Unlock()
		})
	// 把抖动压到毫秒级，否则这个用例要跑十几秒。
	q.minJitter = time.Millisecond
	q.maxJitter = 2 * time.Millisecond

	for _, id := range []string{"a1", "a2", "a3"} {
		q.push(queuedJob{accountID: id, providerID: "p"})
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := len(order)
		mu.Unlock()
		if done == 3 && q.Pending() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()

	if len(got) != 3 {
		t.Fatalf("执行数 = %d, want 3（%v）", len(got), got)
	}
	// 串行队列必须严格保持入队顺序
	want := []string{"a1", "a2", "a3"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("顺序错误: got %v, want %v", got, want)
			break
		}
	}
	q.Stop()
}

func TestHHMMFromCron(t *testing.T) {
	cases := map[string]string{
		"0 9 * * *":    "09:00",
		"30 8 * * 1-5": "08:30",
		"*/15 * * * *": "", // 分钟是表达式，不做时间限制
		"0 0,12 * * *": "", // 小时是列表
		"5 23 * * *":   "23:05",
		"":             "",
	}
	for in, want := range cases {
		if got := hhmmFromCron(in); got != want {
			t.Errorf("hhmmFromCron(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJitterDurationRange(t *testing.T) {
	lo, hi := 2*time.Second, 6*time.Second
	for i := 0; i < 200; i++ {
		d := jitterDuration(lo, hi)
		if d < lo || d > hi {
			t.Fatalf("抖动越界: %v 不在 [%v, %v]", d, lo, hi)
		}
	}
}
