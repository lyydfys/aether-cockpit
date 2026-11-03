package provider

import (
	"testing"
	"time"
)

func TestWindowCapsOfAndHolds(t *testing.T) {
	var none WindowCaps
	if none.Holds() {
		t.Error("零值不该 hold")
	}
	if got := none.Of("weekly"); got != 0 {
		t.Errorf("零值 Of = %d", got)
	}

	c := WindowCaps{All: 90, Windows: map[string]int{"5 Hours": 60, "weekly": 100}}
	if got := c.Of("5 hours"); got != 60 {
		t.Errorf("窗口名归一后应命中 60，got %d", got)
	}
	if got := c.Of("7 days"); got != 90 {
		t.Errorf("未列出的窗口应用 All=90，got %d", got)
	}
	if got := c.Of("weekly"); got != 100 {
		t.Errorf("显式 100 表示该窗口免 cap，got %d", got)
	}
	if !c.Holds() {
		t.Error("有 1..99 的窗口时应 Holds")
	}

	// 只有 100 不算 hold。
	if (WindowCaps{All: 100}).Holds() {
		t.Error("全 100 不该 hold")
	}
}

func TestCapHeldFailOpenWithoutReadings(t *testing.T) {
	e := NewAccountEnv("a1", "p1")
	e.SetWindowCaps(WindowCaps{All: 50})

	// 没有额度读数：不能按住，否则没有额度接口的渠道会被永久挡住。
	if held, _, _ := e.CapHeld(time.Now()); held {
		t.Fatal("没有读数时不该 hold")
	}

	e.SetQuota(&QuotaStatus{
		Windows: []QuotaWindow{{Name: "5h", Used: 80}},
	})
	held, name, share := e.CapHeld(time.Now())
	if !held || name != "5h" || share != 50 {
		t.Fatalf("应命中 5h/50，got held=%v name=%q share=%d", held, name, share)
	}

	// 窗口已过重置时间：视为新一轮，不该再 hold。
	e.SetQuota(&QuotaStatus{
		Windows: []QuotaWindow{{Name: "5h", Used: 80, ResetsAt: time.Now().Add(-time.Minute).Unix()}},
	})
	if held, _, _ := e.CapHeld(time.Now()); held {
		t.Fatal("窗口已重置，不该 hold")
	}

	// 只给 Amount/Limit 时用比例反推。
	e.SetQuota(&QuotaStatus{
		Windows: []QuotaWindow{{Name: "5h", Amount: 95, Limit: 100}},
	})
	if held, _, _ := e.CapHeld(time.Now()); !held {
		t.Fatal("Amount/Limit 反推应 hold")
	}
}

func TestModelRestBackoffLadder(t *testing.T) {
	cases := []struct {
		fails int
		want  time.Duration
	}{
		{1, time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{4, 8 * time.Minute},
		{5, 10 * time.Minute}, // 上限
		{99, 10 * time.Minute},
		{0, time.Minute},
	}
	for _, tc := range cases {
		if got := ModelRestBackoff(tc.fails); got != tc.want {
			t.Errorf("ModelRestBackoff(%d) = %v, want %v", tc.fails, got, tc.want)
		}
	}
}

func TestModelCoolingDownScopedToOneModel(t *testing.T) {
	e := NewAccountEnv("a1", "p1")
	e.MarkModelCoolingDown("claude-sonnet", time.Minute, "429")

	cooling, _ := e.ModelCoolingDown("claude-sonnet")
	if !cooling {
		t.Fatal("该模型应在冷却")
	}
	// 关键：同账号的其它模型不受影响——这正是与账号级冷却的差别。
	if other, _ := e.ModelCoolingDown("gpt-5"); other {
		t.Fatal("其它模型不该被牵连")
	}
	// 账号本身仍可用（模型级不迁移账号状态）。
	if !e.Available() {
		t.Fatal("模型级冷却不该把账号标成不可用")
	}

	e.ClearModelRest("claude-sonnet")
	if cooling, _ := e.ModelCoolingDown("claude-sonnet"); cooling {
		t.Fatal("成功一次后应清除冷却")
	}
}

func TestModelRestExpiresAndRestores(t *testing.T) {
	e := NewAccountEnv("a1", "p1")
	e.MarkModelCoolingDown("m", 10*time.Millisecond, "429")
	time.Sleep(20 * time.Millisecond)
	if cooling, _ := e.ModelCoolingDown("m"); cooling {
		t.Fatal("过期后不该冷却")
	}

	e2 := NewAccountEnv("a2", "p1")
	e2.RestoreModelRests([]ModelRest{
		{Model: "m1", Until: time.Now().Add(time.Hour).Unix(), Reason: "429"},
		{Model: "m2", Until: time.Now().Add(-time.Hour).Unix(), Reason: "stale"},
	})
	if cooling, _ := e2.ModelCoolingDown("m1"); !cooling {
		t.Fatal("未过期条目应恢复")
	}
	if cooling, _ := e2.ModelCoolingDown("m2"); cooling {
		t.Fatal("过期条目不该恢复")
	}
}

func TestFailureBackoffIsBounded(t *testing.T) {
	if got := failureBackoff(1); got != time.Minute {
		t.Errorf("1 次失败 = %v", got)
	}
	if got := failureBackoff(3); got != 4*time.Minute {
		t.Errorf("3 次失败 = %v", got)
	}
	if got := failureBackoff(100); got > 10*time.Minute {
		t.Errorf("上限应 <= 10min，got %v", got)
	}
}

func TestJitterStaysInRange(t *testing.T) {
	base := 10 * time.Minute
	for i := 0; i < 50; i++ {
		got := jitter(base)
		if got < base*8/10 || got > base*12/10 {
			t.Fatalf("抖动越界：%v", got)
		}
	}
}
