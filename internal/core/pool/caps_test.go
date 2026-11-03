package pool

import (
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 触到窗口 cap 的账号应当排在后面，但不被排除：
// 全池都到 cap 时仍然要能发请求，否则等于自己把自己锁死。
func TestSelectOrdersCapHeldLast(t *testing.T) {
	m := New()
	free := provider.NewAccountEnv("free", "p")
	free.SetQuota(&provider.QuotaStatus{Windows: []provider.QuotaWindow{{Name: "5h", Used: 10}}})
	capped := provider.NewAccountEnv("capped", "p")
	capped.SetQuota(&provider.QuotaStatus{Windows: []provider.QuotaWindow{{Name: "5h", Used: 95}}})
	capped.SetWindowCaps(provider.WindowCaps{All: 90})
	m.Add(capped) // 先加 capped，确认排序真的起作用而不是靠加入顺序
	m.Add(free)

	for i := 0; i < 3; i++ {
		got, err := m.Select("p", SelectOptions{Mode: "order"})
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if got.AccountID != "free" {
			t.Fatalf("cap 未触顶的账号应优先，got %s", got.AccountID)
		}
	}
}

// 全池都被 cap 时必须仍能选出账号（降级而不是拒绝）。
func TestSelectAllCappedStillSelects(t *testing.T) {
	m := New()
	a := provider.NewAccountEnv("a", "p")
	a.SetQuota(&provider.QuotaStatus{Windows: []provider.QuotaWindow{{Name: "5h", Used: 99}}})
	a.SetWindowCaps(provider.WindowCaps{All: 50})
	m.Add(a)

	got, err := m.Select("p", SelectOptions{Mode: "order"})
	if err != nil {
		t.Fatalf("全池 cap 时不该直接失败：%v", err)
	}
	if got == nil || got.AccountID != "a" {
		t.Fatalf("应降级选到 a，got %v", got)
	}
}

// 模型级冷却只影响那个模型，同账号的其它模型照常被选中。
func TestSelectModelRestScopedToOneModel(t *testing.T) {
	m := New()
	a := provider.NewAccountEnv("a", "p")
	b := provider.NewAccountEnv("b", "p")
	a.MarkModelCoolingDown("only-this-model", time.Minute, "429")
	m.Add(a)
	m.Add(b)

	// 被冷却的模型：应优先选 b。
	got, err := m.Select("p", SelectOptions{Mode: "order", Model: "only-this-model"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.AccountID != "b" {
		t.Fatalf("该模型在 a 上冷却，应选 b，got %s", got.AccountID)
	}

	// 其它模型：a 仍然可用，且排在前面（order + 先加入）。
	got, err = m.Select("p", SelectOptions{Mode: "order", Model: "another-model"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.AccountID != "a" {
		t.Fatalf("其它模型不该被牵连，应选 a，got %s", got.AccountID)
	}
}

// 冷却是"排序偏好"而不是"不可用"：两个号都在该模型上冷却时仍要选出一个。
func TestSelectAllModelRestedStillSelects(t *testing.T) {
	m := New()
	a := provider.NewAccountEnv("a", "p")
	b := provider.NewAccountEnv("b", "p")
	a.MarkModelCoolingDown("m", time.Minute, "429")
	b.MarkModelCoolingDown("m", time.Minute, "429")
	m.Add(a)
	m.Add(b)

	if _, err := m.Select("p", SelectOptions{Mode: "order", Model: "m"}); err != nil {
		t.Fatalf("全池模型冷却时不该直接失败：%v", err)
	}
}
