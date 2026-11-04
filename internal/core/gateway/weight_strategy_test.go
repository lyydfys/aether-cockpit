package gateway

import (
	"testing"
)

func weightSeats(ids ...string) []modelBinding {
	out := make([]modelBinding, 0, len(ids))
	for _, id := range ids {
		out = append(out, modelBinding{ProviderID: id, UpstreamModel: "m", DisplayName: id})
	}
	return out
}

// 权重 2:1 时的前 6 次命中应当是平滑的 a,b,a,a,b,a（而不是 a,a,a,a,b,b）。
func TestStrategyWeightSmoothDistribution(t *testing.T) {
	r := NewModelRouter()
	weights := map[string]int{"a": 2, "b": 1}
	ctx := &RouteContext{
		Group:  "g",
		Weight: func(pid string) int { return weights[pid] },
		Wrr:    r.wrrNext,
	}
	seats := weightSeats("a", "b")

	var order []string
	for i := 0; i < 6; i++ {
		got := strategyWeight(ctx, seats)
		if len(got) != 2 {
			t.Fatalf("结果应是输入的排列，得到 %+v", got)
		}
		order = append(order, got[0].ProviderID)
	}
	// 6 次里 a 应出现 4 次、b 出现 2 次（份额 2:1）。
	count := map[string]int{}
	for _, id := range order {
		count[id]++
	}
	if count["a"] != 4 || count["b"] != 2 {
		t.Fatalf("份额不对：%v（顺序 %v）", count, order)
	}
	// 平滑性：不允许出现连续 3 次同一个。
	run, prev := 1, order[0]
	for _, id := range order[1:] {
		if id == prev {
			run++
			if run >= 3 {
				t.Fatalf("出现连续 3 次同一渠道：%v", order)
			}
		} else {
			run, prev = 1, id
		}
	}
}

// 没有权重来源 / 没有轮询状态时保持原序（fail-open，不自己造顺序）。
func TestStrategyWeightFallsBackToInputOrder(t *testing.T) {
	seats := weightSeats("x", "y", "z")
	if got := strategyWeight(&RouteContext{Group: "g"}, seats); got[0].ProviderID != "x" {
		t.Fatalf("无权重来源应保持原序：%+v", got)
	}
	if got := strategyWeight(nil, seats); got[0].ProviderID != "x" {
		t.Fatalf("nil ctx 应保持原序：%+v", got)
	}
	// 单候选直接返回。
	if got := strategyWeight(&RouteContext{}, weightSeats("solo")); len(got) != 1 {
		t.Fatal("单候选应原样返回")
	}
}

// 权重 0 / 负数按 1 处理：不能等于"永不命中"。
func TestStrategyWeightTreatsZeroAsOne(t *testing.T) {
	r := NewModelRouter()
	weights := map[string]int{"a": 0, "b": -5}
	calls := map[string]int{}
	ctx := &RouteContext{
		Group: "g",
		Weight: func(pid string) int {
			calls[pid]++
			return weights[pid]
		},
		Wrr: r.wrrNext,
	}
	seats := weightSeats("a", "b")
	hit := map[string]int{}
	for i := 0; i < 4; i++ {
		hit[strategyWeight(ctx, seats)[0].ProviderID]++
	}
	if hit["a"] != 2 || hit["b"] != 2 {
		t.Fatalf("0/负数应等价于 1（各 2 次），得到 %v", hit)
	}
}

// 状态按组隔离：两个组各轮各的，互不干扰。
func TestWRRStateIsolatedPerGroup(t *testing.T) {
	r := NewModelRouter()
	first := r.wrrNext("g1", []string{"a", "b"}, []int{1, 1})
	// 另一个组第一次也应从 0 开始（a 先）。
	other := r.wrrNext("g2", []string{"a", "b"}, []int{1, 1})
	if first != other {
		t.Fatalf("不同组应各自从同一起点开始：g1=%d g2=%d", first, other)
	}
	if second := r.wrrNext("g1", []string{"a", "b"}, []int{1, 1}); second != 1 {
		t.Fatalf("同组第二次应轮到另一个候选，得到 %d", second)
	}
}

// 权重和渠道集合变化时不能越界。
func TestWRRHandlesShapeChange(t *testing.T) {
	r := NewModelRouter()
	if idx := r.wrrNext("g", []string{"a"}, []int{3}); idx != 0 {
		t.Fatalf("单候选应返回 0，得到 %d", idx)
	}
	if idx := r.wrrNext("g", []string{"a", "b", "c"}, []int{1, 2, 3}); idx < 0 || idx > 2 {
		t.Fatalf("下标越界：%d", idx)
	}
	if idx := r.wrrNext("g", nil, nil); idx != 0 {
		t.Fatalf("空输入应返回 0，得到 %d", idx)
	}
	if idx := r.wrrNext("g", []string{"a", "b"}, []int{1}); idx != 0 {
		t.Fatalf("长度不匹配应返回 0，得到 %d", idx)
	}
}
