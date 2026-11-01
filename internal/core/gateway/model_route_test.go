package gateway

import (
	"os"
	"testing"
	"time"

	"aether-cockpit/internal/providers/mock"
)

// mock 渠道不再随包 init 注册（产品运行时不带假上游），测试显式注册一次。
func TestMain(m *testing.M) {
	mock.Register()
	os.Exit(m.Run())
}

func newTestRouter() *ModelRouter {
	r := NewModelRouter()
	r.Rebuild()
	return r
}

func TestResolveExact(t *testing.T) {
	r := newTestRouter()
	got := r.Resolve("mock-native/echo-pro")
	if len(got) != 1 {
		t.Fatalf("候选数 = %d, want 1", len(got))
	}
	if got[0].ProviderID != "mock-native" || got[0].UpstreamModel != "echo-pro" {
		t.Errorf("绑定错误: %+v", got[0])
	}
}

func TestResolveBareName(t *testing.T) {
	r := newTestRouter()
	got := r.Resolve("echo-pro")
	if len(got) != 1 || got[0].ProviderID != "mock-native" {
		t.Fatalf("裸名解析失败: %+v", got)
	}
}

// TestResolveUnknownIsEmpty 是最重要的一条：
// 未知模型必须返回空候选，让网关报错，而不是静默路由到某个渠道。
//
// 静默兜底会让"模型名拼错"表现为"成功但用了错的模型"，
// 这比直接报错危险得多 —— 用户会产生完全错误的判断。
func TestResolveUnknownIsEmpty(t *testing.T) {
	r := newTestRouter()
	got := r.Resolve("完全不存在-xyz-123")
	if len(got) != 0 {
		t.Errorf("未知模型应返回空候选，实际 %+v", got)
	}
}

func TestResolvePrefixPassthrough(t *testing.T) {
	r := newTestRouter()
	// 带已注册渠道前缀时，允许透传未登记的模型名（用户明确指定了渠道）。
	got := r.Resolve("mock-native/some-future-model")
	if len(got) != 1 {
		t.Fatalf("前缀透传失败: %+v", got)
	}
	if got[0].UpstreamModel != "some-future-model" {
		t.Errorf("上游模型 = %q", got[0].UpstreamModel)
	}
}

func TestResolveUnknownPrefixIsEmpty(t *testing.T) {
	r := newTestRouter()
	// 前缀不是已注册渠道 → 不应透传
	got := r.Resolve("nope-provider/some-model")
	if len(got) != 0 {
		t.Errorf("未知渠道前缀应返回空，实际 %+v", got)
	}
}

func TestModelFamily(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-4-6-20260101": "claude",
		"gpt-5-turbo":                "gpt",
		"deepseek-v3":                "deepseek",
		"qwen3-coder-plus":           "qwen",
		"unknown-model":              "",
	}
	for in, want := range cases {
		if got := modelFamily(in); got != want {
			t.Errorf("modelFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAvailableModelIDs(t *testing.T) {
	r := newTestRouter()
	// 0 = 不截断：目录随厂商增多而变大，前 N 条不再能代表全量。
	ids := r.AvailableModelIDs(0)
	if len(ids) == 0 {
		t.Fatal("应至少有一个已登记模型")
	}
	found := false
	for _, id := range ids {
		if id == "mock-native/echo-pro" {
			found = true
		}
	}
	if !found {
		t.Errorf("缺少 mock-native/echo-pro，实际 %v", ids)
	}
}

func TestVirtualGroupResolve(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroup("auto/free", []string{"mock-native", "mock-weak"})

	got := r.Resolve("auto/free")
	if len(got) != 2 {
		t.Fatalf("候选数 = %d, want 2", len(got))
	}
	// 必须严格按配置的优先级顺序
	if got[0].ProviderID != "mock-native" {
		t.Errorf("首位应为 mock-native，实际 %s", got[0].ProviderID)
	}
	if got[1].ProviderID != "mock-weak" {
		t.Errorf("次位应为 mock-weak，实际 %s", got[1].ProviderID)
	}
	// 上游模型取该渠道 Manifest 里登记的第一个
	if got[0].UpstreamModel != "echo-pro" {
		t.Errorf("上游模型 = %q, want echo-pro", got[0].UpstreamModel)
	}
}

func TestVirtualGroupSkipsUnknownProvider(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroup("auto/x", []string{"不存在的渠道", "mock-native"})

	got := r.Resolve("auto/x")
	if len(got) != 1 {
		t.Fatalf("候选数 = %d, want 1（未知渠道应被跳过）", len(got))
	}
	if got[0].ProviderID != "mock-native" {
		t.Errorf("剩余候选错误: %+v", got[0])
	}
}

func TestVirtualGroupEmptyChainFallsThrough(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroup("auto/empty", []string{"不存在的渠道"})
	// 组内全是无效渠道时应回落到常规解析，而不是返回空导致 400
	got := r.Resolve("auto/empty")
	if len(got) != 0 {
		t.Errorf("无效虚拟组应返回空，实际 %+v", got)
	}
}

func TestVirtualGroupsSnapshot(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroup("auto/a", []string{"mock-native"})
	groups := r.VirtualGroups()
	if len(groups["auto/a"]) != 1 {
		t.Errorf("虚拟组快照错误: %+v", groups)
	}
}

func TestLogBuffer(t *testing.T) {
	b := NewLogBuffer(3)
	for i := 0; i < 5; i++ {
		b.Push(RequestLog{Status: "ok", DurationMS: int64(i * 10)})
	}
	recent := b.Recent(10)
	if len(recent) != 3 {
		t.Fatalf("容量 3 应只保留 3 条，实际 %d", len(recent))
	}
	// 最新的一条应该在首位
	if recent[0].DurationMS != 40 {
		t.Errorf("首条应为最新记录，实际 %d", recent[0].DurationMS)
	}
	if recent[0].Seq != 5 {
		t.Errorf("序号应为 5，实际 %d", recent[0].Seq)
	}

	st := b.Stats()
	if st.Total != 3 || st.OK != 3 || st.Errors != 0 {
		t.Errorf("统计错误: %+v", st)
	}
}

// ---- pace 策略 ----

// quotaStub 是单渠道的额度桩。
type quotaStub struct {
	remaining int
	resetAt   int64
}

// paceTestCtx 构造带额度桩的 RouteContext：不在表里的渠道 ok=false。
func paceTestCtx(q map[string]quotaStub) *RouteContext {
	return &RouteContext{Quota: func(pid string) (int, int64, bool) {
		s, ok := q[pid]
		if !ok {
			return 0, 0, false
		}
		return s.remaining, s.resetAt, true
	}}
}

// TestStrategyPaceOrder 核心排序：额度大且临期的排第一，
// rate 递减，无额度数据的渠道垫底。
func TestStrategyPaceOrder(t *testing.T) {
	now := time.Now().Unix()
	seats := []modelBinding{
		{ProviderID: "p-mid"},  // 100 / 1h   = 100
		{ProviderID: "p-none"}, // 无额度数据
		{ProviderID: "p-big"},  // 400 / 0.5h = 800（重置前 30 分钟，钳到 0.5h）
		{ProviderID: "p-low"},  // 100 / 20h  = 5
	}
	ctx := paceTestCtx(map[string]quotaStub{
		"p-big": {400, now + 1800},
		"p-mid": {100, now + 3600},
		"p-low": {100, now + 20*3600},
	})
	got := strategyPace(ctx, seats)
	want := []string{"p-big", "p-mid", "p-low", "p-none"}
	for i, id := range want {
		if got[i].ProviderID != id {
			t.Fatalf("pace 排序 = %v, want %v", providerIDsOf(got), want)
		}
	}
}

// TestStrategyPaceStableWithinBand 同带（rate 差 <10%）必须保持原序。
// 100 与 104 落在同一个 10% 等比带内。
func TestStrategyPaceStableWithinBand(t *testing.T) {
	now := time.Now().Unix()
	seats := []modelBinding{{ProviderID: "p-a"}, {ProviderID: "p-b"}}
	ctx := paceTestCtx(map[string]quotaStub{
		"p-a": {100, now + 3600},
		"p-b": {104, now + 3600},
	})
	got := strategyPace(ctx, seats)
	if got[0].ProviderID != "p-a" || got[1].ProviderID != "p-b" {
		t.Fatalf("同带应保持原序，实际 %v", providerIDsOf(got))
	}
}

// TestStrategyPaceMissingResetAt resetAt 缺省/已过的分支：
// hours 钳到 0.5，remaining 原值参与比较。
func TestStrategyPaceMissingResetAt(t *testing.T) {
	now := time.Now().Unix()
	seats := []modelBinding{{ProviderID: "p-hour"}, {ProviderID: "p-zero"}, {ProviderID: "p-past"}}
	ctx := paceTestCtx(map[string]quotaStub{
		"p-hour": {100, now + 3600}, // rate 100
		"p-zero": {100, 0},          // 视为 0.5h → rate 200
		"p-past": {100, now - 60},   // 已重置 → 视为 0.5h → rate 200
	})
	got := strategyPace(ctx, seats)
	want := []string{"p-zero", "p-past", "p-hour"} // zero 与 past 同带，保持原序
	for i, id := range want {
		if got[i].ProviderID != id {
			t.Fatalf("resetAt 缺省排序 = %v, want %v", providerIDsOf(got), want)
		}
	}
}

// TestStrategyPaceAllMissing 整组无额度数据时保持原序（fail-open）。
func TestStrategyPaceAllMissing(t *testing.T) {
	seats := []modelBinding{{ProviderID: "a"}, {ProviderID: "b"}, {ProviderID: "c"}}
	got := strategyPace(&RouteContext{}, seats)
	for i, b := range seats {
		if got[i].ProviderID != b.ProviderID {
			t.Fatalf("无额度数据应保持原序，实际 %v", providerIDsOf(got))
		}
	}
}

// TestResolvePaceIntegration 集成：SetQuotaLookup 注入假额度后，
// Resolve 的虚拟组按 pace 序返回；无额度数据时保持配置序。
func TestResolvePaceIntegration(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroupWithStrategy("auto/pace", []string{"mock-native", "mock-weak"}, GroupStrategyPace)

	// 未注入额度：pace 退化为原序。
	got := r.Resolve("auto/pace")
	if len(got) != 2 || got[0].ProviderID != "mock-native" {
		t.Fatalf("无额度数据应保持配置序，实际 %v", providerIDsOf(got))
	}

	r.SetQuotaLookup(func(providerID string) (int, int64, bool) {
		switch providerID {
		case "mock-weak":
			// 临期且剩余多：rate 1000，遥遥领先。
			return 500, time.Now().Add(30 * time.Minute).Unix(), true
		case "mock-native":
			// 剩余少且远期：rate 1。
			return 10, time.Now().Add(10 * time.Hour).Unix(), true
		}
		return 0, 0, false
	})

	got = r.Resolve("auto/pace")
	if len(got) != 2 || got[0].ProviderID != "mock-weak" || got[1].ProviderID != "mock-native" {
		t.Fatalf("pace 应把临期大额度渠道排前，实际 %v", providerIDsOf(got))
	}

	// 撤掉额度查询：回到配置序。
	r.SetQuotaLookup(nil)
	got = r.Resolve("auto/pace")
	if got[0].ProviderID != "mock-native" {
		t.Fatalf("撤掉额度查询应回到配置序，实际 %v", providerIDsOf(got))
	}
}

// ---- smart 策略（意图路由）----

// smartSeats 三渠道链：p-strong → p-cheap → p-fallback。
var smartSeats = []modelBinding{
	{ProviderID: "p-strong", UpstreamModel: "m"},
	{ProviderID: "p-cheap", UpstreamModel: "m"},
	{ProviderID: "p-fallback", UpstreamModel: "m"},
}

// smartIntentStub 返回固定下标的分类函数。
func smartIntentStub(idx int, calls *int) IntentClassifyFunc {
	return func(group, sessionKey string, seats []modelBinding, text string) int {
		*calls++
		return idx
	}
}

// TestStrategySmartMovesToFront 命中下标 2 → 该渠道提前，其余保持相对顺序。
// fallback 链语义必须保留。
func TestStrategySmartMovesToFront(t *testing.T) {
	calls := 0
	ctx := &RouteContext{
		Group: "auto/x", Text: "帮我写个爬虫", SessionKey: "sk1",
		Intent: func(group, sessionKey string, seats []modelBinding, text string) int {
			calls++
			if group != "auto/x" || sessionKey != "sk1" || text != "帮我写个爬虫" || len(seats) != 3 {
				t.Errorf("分类函数入参错误: %q %q %q %d", group, sessionKey, text, len(seats))
			}
			return 2
		},
	}
	got := applyRouteStrategy(GroupStrategySmart, ctx, smartSeats)
	want := []string{"p-fallback", "p-strong", "p-cheap"}
	for i, id := range want {
		if got[i].ProviderID != id {
			t.Fatalf("smart 排序 = %v, want %v", providerIDsOf(got), want)
		}
	}
	if calls != 1 {
		t.Fatalf("分类应恰好调用一次，实际 %d", calls)
	}
}

// TestStrategySmartKeepsOrder 无意图 / 0 / 负数 / 越界时一律维持原序。
func TestStrategySmartKeepsOrder(t *testing.T) {
	cases := []int{0, -1, 3, 99}
	for _, idx := range cases {
		ctx := &RouteContext{Group: "g", Intent: smartIntentStub(idx, new(int))}
		got := applyRouteStrategy(GroupStrategySmart, ctx, smartSeats)
		for i, b := range smartSeats {
			if got[i].ProviderID != b.ProviderID {
				t.Fatalf("idx=%d 应保持原序，实际 %v", idx, providerIDsOf(got))
			}
		}
	}
	// Intent 未注入（未配置分类模型 / 防递归路径）：nil → 原序。
	got := applyRouteStrategy(GroupStrategySmart, &RouteContext{Group: "g"}, smartSeats)
	for i, b := range smartSeats {
		if got[i].ProviderID != b.ProviderID {
			t.Fatalf("无 Intent 应保持原序，实际 %v", providerIDsOf(got))
		}
	}
	// ctx 本身为 nil 的防御路径。
	got = applyRouteStrategy(GroupStrategySmart, nil, smartSeats)
	if got[0].ProviderID != "p-strong" {
		t.Fatalf("nil ctx 应保持原序，实际 %v", providerIDsOf(got))
	}
}

// TestStrategySmartPanicRecovered 分类函数 panic 必须被
// applyRouteStrategy recover 并退回原序——smart 走的是同一套 fail-open。
func TestStrategySmartPanicRecovered(t *testing.T) {
	ctx := &RouteContext{Group: "g", Intent: func(group, sessionKey string, seats []modelBinding, text string) int {
		panic("分类器炸了")
	}}
	got := applyRouteStrategy(GroupStrategySmart, ctx, smartSeats)
	for i, b := range smartSeats {
		if got[i].ProviderID != b.ProviderID {
			t.Fatalf("panic 后应退回原序，实际 %v", providerIDsOf(got))
		}
	}
}

// TestResolveSmartEndToEnd 集成：smart 组 + 注入分类器 → ResolveSmart
// 按分类结果重排；普通 Resolve 不受影响。
func TestResolveSmartEndToEnd(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroupWithStrategy("auto/smart", []string{"mock-native", "mock-weak"}, GroupStrategySmart)

	calls := 0
	r.SetIntentClassifier(func(group, sessionKey string, seats []modelBinding, text string) int {
		calls++
		for i, b := range seats {
			if b.ProviderID == "mock-weak" {
				return i
			}
		}
		return 0
	})

	got := r.ResolveSmart("auto/smart", "这是一段足够长的用户提问，用来触发意图分类的逻辑路径", "sk1", false)
	if len(got) != 2 || got[0].ProviderID != "mock-weak" {
		t.Fatalf("ResolveSmart 应把分类命中的渠道提前，实际 %v", providerIDsOf(got))
	}
	if calls != 1 {
		t.Fatalf("分类应被调用一次，实际 %d", calls)
	}

	// 普通 Resolve 不带意图：保持配置序。
	if got := r.Resolve("auto/smart"); got[0].ProviderID != "mock-native" {
		t.Fatalf("Resolve 不应受意图影响，实际 %v", providerIDsOf(got))
	}
	if calls != 1 {
		t.Fatalf("Resolve 不应触发分类，实际调用 %d 次", calls)
	}
}

// TestResolveSmartFromRouterSkipsIntent 防递归：带 X-Aether-Intent 的
// 请求（fromRouter=true）绝不注入 Intent——否则分类请求自己又触发分类。
func TestResolveSmartFromRouterSkipsIntent(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroupWithStrategy("auto/smart", []string{"mock-native", "mock-weak"}, GroupStrategySmart)
	r.SetIntentClassifier(func(group, sessionKey string, seats []modelBinding, text string) int {
		t.Error("分类请求不应再触发分类（死循环防线被突破）")
		return 1
	})

	got := r.ResolveSmart("auto/smart", "分类模型的提问", "sk1", true)
	if len(got) != 2 || got[0].ProviderID != "mock-native" {
		t.Fatalf("fromRouter 应保持原序，实际 %v", providerIDsOf(got))
	}

	// 空文本同样不分类。
	got = r.ResolveSmart("auto/smart", "", "sk1", false)
	if got[0].ProviderID != "mock-native" {
		t.Fatalf("空文本应保持原序，实际 %v", providerIDsOf(got))
	}
}

// TestResolveSmartExactModelSkipsIntent 精确模型不走策略，分类器不该被调。
func TestResolveSmartExactModelSkipsIntent(t *testing.T) {
	r := newTestRouter()
	r.SetIntentClassifier(func(group, sessionKey string, seats []modelBinding, text string) int {
		t.Error("精确模型不应触发分类")
		return 1
	})
	got := r.ResolveSmart("mock-native/echo-pro", "一段足够长的提问文本，超过短追问阈值以上", "sk1", false)
	if len(got) != 1 || got[0].ProviderID != "mock-native" {
		t.Fatalf("精确解析失败: %+v", got)
	}
}

// TestSetIntentClassifierNilDisables 传 nil 撤销分类器，smart 退化为 priority。
func TestSetIntentClassifierNilDisables(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroupWithStrategy("auto/smart", []string{"mock-native", "mock-weak"}, GroupStrategySmart)
	r.SetIntentClassifier(func(group, sessionKey string, seats []modelBinding, text string) int { return 1 })
	r.SetIntentClassifier(nil)
	got := r.ResolveSmart("auto/smart", "一段足够长的用户提问，用来触发意图分类的逻辑路径", "sk1", false)
	if got[0].ProviderID != "mock-native" {
		t.Fatalf("撤销分类器后应保持配置序，实际 %v", providerIDsOf(got))
	}
}

// fast 策略：TTFT 升序、并列看吞吐、无样本的渠道垫底且保持原序。
func TestStrategyFastOrdersByLatency(t *testing.T) {
	seats := []modelBinding{
		{ProviderID: "slow"}, {ProviderID: "unknown"}, {ProviderID: "fast"}, {ProviderID: "mid"},
	}
	ctx := &RouteContext{Latency: func(pid string) (float64, float64, bool) {
		switch pid {
		case "fast":
			return 120, 40, true
		case "mid":
			return 400, 80, true
		case "slow":
			return 900, 10, true
		}
		return 0, 0, false
	}}
	got := strategyFast(ctx, seats)
	want := []string{"fast", "mid", "slow", "unknown"}
	for i, w := range want {
		if got[i].ProviderID != w {
			t.Fatalf("顺序 = %v, want %v", providerIDsOf(got), want)
		}
	}

	// 没有时延数据时必须原序返回（fail-open）。
	if plain := strategyFast(&RouteContext{}, seats); providerIDsOf(plain)[0] != "slow" {
		t.Fatalf("无样本应保持原序: %v", providerIDsOf(plain))
	}
	// nil ctx 也不能崩。
	if plain := strategyFast(nil, seats); len(plain) != len(seats) {
		t.Fatal("nil ctx 返回长度不对")
	}
}

func providerIDsOf(bs []modelBinding) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ProviderID
	}
	return out
}
