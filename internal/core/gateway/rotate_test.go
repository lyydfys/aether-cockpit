package gateway

import (
	"encoding/json"
	"testing"
)

func TestParseModelGroupBothForms(t *testing.T) {
	chain, strategy, err := parseModelGroup(json.RawMessage(`["a","b"]`))
	if err != nil || strategy != GroupStrategyPriority || len(chain) != 2 {
		t.Errorf("数组形态解析失败: %v %q %v", chain, strategy, err)
	}

	chain, strategy, err = parseModelGroup(json.RawMessage(`{"chain":["a","b","c"],"strategy":"rotate"}`))
	if err != nil || strategy != GroupStrategyRotate || len(chain) != 3 {
		t.Errorf("对象形态解析失败: %v %q %v", chain, strategy, err)
	}

	if _, _, err := parseModelGroup(json.RawMessage(`{"bogus": 1}`)); err == nil {
		t.Error("非法形态应报错")
	}
}

// TestStrategyRotatePure 验证 rotate 策略的纯排列行为：
// 给定偏移即可复现旧 rotateBindings 的轮转序列（0,1,2,0,...）。
func TestStrategyRotatePure(t *testing.T) {
	mk := func() []modelBinding {
		return []modelBinding{{ProviderID: "p1"}, {ProviderID: "p2"}, {ProviderID: "p3"}}
	}
	offsets := []int{0, 1, 2, 0, 1}
	want := [][]string{
		{"p1", "p2", "p3"},
		{"p2", "p3", "p1"},
		{"p3", "p1", "p2"},
		{"p1", "p2", "p3"},
		{"p2", "p3", "p1"},
	}
	for i, off := range offsets {
		ctx := &RouteContext{Rotate: func(int) int { return off }}
		got := strategyRotate(ctx, mk())
		if len(got) != 3 {
			t.Fatalf("第 %d 次轮转长度错误: %v", i+1, ids(got))
		}
		for j, id := range want[i] {
			if got[j].ProviderID != id {
				t.Fatalf("偏移 %d 的轮转 = %v, want %v", off, ids(got), want[i])
			}
		}
	}

	// n<=0 时 Rotate 契约返回 0，空列表应原样通过。
	if got := strategyRotate(&RouteContext{}, nil); len(got) != 0 {
		t.Errorf("空列表应原样返回，实际 %v", ids(got))
	}
}

// TestPriorityStrategyPure priority 策略必须原样返回。
func TestPriorityStrategyPure(t *testing.T) {
	seats := []modelBinding{{ProviderID: "p1"}, {ProviderID: "p2"}}
	got := strategyPriority(&RouteContext{}, seats)
	if got[0].ProviderID != "p1" || got[1].ProviderID != "p2" {
		t.Fatalf("priority 策略不应重排: %v", ids(got))
	}
}

// TestRotateThroughResolve 验证计数器迁移进框架后，
// Resolve 路径上的轮转序列与旧 rotateBindings 一致。
func TestRotateThroughResolve(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroupWithStrategy("auto/rot", []string{"mock-native", "mock-weak"}, GroupStrategyRotate)
	if got := r.GroupStrategy("auto/rot"); got != GroupStrategyRotate {
		t.Fatalf("GroupStrategy = %q", got)
	}

	want := [][]string{
		{"mock-native", "mock-weak"},
		{"mock-weak", "mock-native"},
		{"mock-native", "mock-weak"},
	}
	for i, w := range want {
		got := r.Resolve("auto/rot")
		if len(got) != len(w) {
			t.Fatalf("第 %d 次解析候选数 = %d, want %d", i+1, len(got), len(w))
		}
		for j, id := range w {
			if got[j].ProviderID != id {
				t.Fatalf("第 %d 次解析 = %v, want %v", i+1, ids(got), w)
			}
		}
	}
}

// TestPriorityGroupResolveKeepsOrder priority 组连续解析顺序不变。
func TestPriorityGroupResolveKeepsOrder(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroupWithStrategy("auto/pri", []string{"mock-native", "mock-weak"}, GroupStrategyPriority)
	for i := 0; i < 3; i++ {
		got := r.Resolve("auto/pri")
		if got[0].ProviderID != "mock-native" || got[1].ProviderID != "mock-weak" {
			t.Fatalf("priority 策略不应轮转: %v", ids(got))
		}
	}
}

// TestSetVirtualGroupUnknownStrategyFallsBack 未注册的策略名回落 priority。
func TestSetVirtualGroupUnknownStrategyFallsBack(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroupWithStrategy("auto/u", []string{"mock-native"}, "weighted")
	if got := r.GroupStrategy("auto/u"); got != GroupStrategyPriority {
		t.Fatalf("未知策略应回落 priority，实际 %q", got)
	}
}

// ---- applyRouteStrategy 框架行为：fail-open 与排列校验 ----

// mustRegister 注册临时测试策略，返回清理函数。
func mustRegister(t *testing.T, name string, fn RouteStrategy) func() {
	t.Helper()
	registerRouteStrategy(name, fn)
	return func() { delete(routeStrategies, name) }
}

// 策略 panic 必须被框架 recover 并退回原序：路由是必经之路，
// 有 bug 的策略不能把请求打挂。
func TestApplyRouteStrategyRecoversPanic(t *testing.T) {
	cleanup := mustRegister(t, "test-panic", func(*RouteContext, []modelBinding) []modelBinding {
		panic("boom")
	})
	defer cleanup()

	seats := []modelBinding{{ProviderID: "a"}, {ProviderID: "b"}}
	got := applyRouteStrategy("test-panic", &RouteContext{}, seats)
	if len(got) != 2 || got[0].ProviderID != "a" || got[1].ProviderID != "b" {
		t.Fatalf("panic 应退回原序，实际 %v", ids(got))
	}
	if seats[0].ProviderID != "a" {
		t.Fatalf("原切片不应被破坏: %v", ids(seats))
	}
}

// 策略返回丢渠道 / 篡改绑定的结果时，框架必须拒收并退回原序。
func TestApplyRouteStrategyRejectsNonPermutation(t *testing.T) {
	seats := []modelBinding{{ProviderID: "a"}, {ProviderID: "b"}}

	cleanupDrop := mustRegister(t, "test-drop", func(_ *RouteContext, s []modelBinding) []modelBinding {
		return s[:1] // 丢渠道：排后面的上游将永远收不到流量
	})
	defer cleanupDrop()
	if got := applyRouteStrategy("test-drop", &RouteContext{}, seats); len(got) != 2 {
		t.Fatalf("丢渠道应被拒并退回原序，实际 %v", ids(got))
	}

	cleanupTamper := mustRegister(t, "test-tamper", func(_ *RouteContext, s []modelBinding) []modelBinding {
		out := make([]modelBinding, len(s))
		copy(out, s)
		out[1].ProviderID = "ghost" // 私自改绑定
		return out
	})
	defer cleanupTamper()
	got := applyRouteStrategy("test-tamper", &RouteContext{}, seats)
	if got[1].ProviderID != "b" {
		t.Fatalf("篡改绑定应被拒并退回原序，实际 %v", ids(got))
	}
}

// 正常策略的返回应被原样采纳。
func TestApplyRouteStrategyAcceptsPermutation(t *testing.T) {
	cleanup := mustRegister(t, "test-reverse", func(_ *RouteContext, s []modelBinding) []modelBinding {
		out := make([]modelBinding, len(s))
		for i, b := range s {
			out[len(s)-1-i] = b
		}
		return out
	})
	defer cleanup()

	seats := []modelBinding{{ProviderID: "a"}, {ProviderID: "b"}, {ProviderID: "c"}}
	got := applyRouteStrategy("test-reverse", &RouteContext{}, seats)
	if got[0].ProviderID != "c" || got[2].ProviderID != "a" {
		t.Fatalf("合法排列应被采纳，实际 %v", ids(got))
	}
}

// 未注册策略名按 priority 原序处理（fail-open 兜底）。
func TestApplyRouteStrategyUnknownName(t *testing.T) {
	seats := []modelBinding{{ProviderID: "a"}, {ProviderID: "b"}}
	got := applyRouteStrategy("不存在", &RouteContext{}, seats)
	if got[0].ProviderID != "a" || got[1].ProviderID != "b" {
		t.Fatalf("未知策略应原序返回，实际 %v", ids(got))
	}
}

func ids(bs []modelBinding) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ProviderID
	}
	return out
}
