package gateway

import (
	"testing"
	"time"
)

// ruleSeats 是三条渠道的候选链，测试只关心 ProviderID 顺序。
func ruleSeats() []modelBinding {
	return []modelBinding{
		{ProviderID: "p-strong", UpstreamModel: "m-strong"},
		{ProviderID: "p-cheap", UpstreamModel: "m-cheap"},
		{ProviderID: "p-mid", UpstreamModel: "m-mid"},
	}
}

func mustDate(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("解析时间 %q: %v", value, err)
	}
	return at
}

// ---- TimeWindow ----

// TestTimeWindowHolds 覆盖跨午夜、时区、星期与跨午夜的日归属。
func TestTimeWindowHolds(t *testing.T) {
	crossMidnight := TimeWindow{From: "22:00", To: "06:00", Zone: "Asia/Shanghai"}
	workHours := TimeWindow{From: "09:00", To: "18:00", Days: []int{1, 2, 3, 4, 5}, Zone: "UTC"}
	fridayNight := TimeWindow{From: "22:00", To: "06:00", Days: []int{5}, Zone: "Asia/Shanghai"} // 周五

	cases := []struct {
		name string
		w    TimeWindow
		at   time.Time
		want bool
	}{
		{"跨午夜-午夜前", crossMidnight, mustDate(t, "2026-01-02T23:30:00+08:00"), true},
		{"跨午夜-凌晨", crossMidnight, mustDate(t, "2026-01-03T02:00:00+08:00"), true},
		{"跨午夜-白天不命中", crossMidnight, mustDate(t, "2026-01-02T12:00:00+08:00"), false},
		{"跨午夜-边界结束不命中", crossMidnight, mustDate(t, "2026-01-03T06:00:00+08:00"), false},
		{"工作日-周五命中", workHours, mustDate(t, "2026-01-02T10:00:00Z"), true},
		{"工作日-周六不命中", workHours, mustDate(t, "2026-01-03T10:00:00Z"), false},
		{"工作日-区间外不命中", workHours, mustDate(t, "2026-01-02T08:59:00Z"), false},
		{"跨午夜星期归属-周六凌晨仍算周五", fridayNight, mustDate(t, "2026-01-03T02:00:00+08:00"), true},
		{"跨午夜星期归属-周五凌晨属于周四", fridayNight, mustDate(t, "2026-01-02T02:00:00+08:00"), false},
		{"跨午夜星期归属-周五深夜", fridayNight, mustDate(t, "2026-01-02T23:00:00+08:00"), true},
		{"全天", TimeWindow{From: "00:00", To: "00:00", Zone: "UTC"}, mustDate(t, "2026-01-03T03:04:00Z"), true},
		{"未知时区不成立", TimeWindow{From: "09:00", To: "18:00", Zone: "Mars/Olympus"}, mustDate(t, "2026-01-02T10:00:00Z"), false},
		{"零值时间不成立", workHours, time.Time{}, false},
		{"时段写法非法不成立", TimeWindow{From: "9点", To: "18:00"}, mustDate(t, "2026-01-02T10:00:00Z"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.w.Holds(tc.at); got != tc.want {
				t.Fatalf("Holds(%s) = %v, want %v", tc.at, got, tc.want)
			}
		})
	}
}

// TestTimeWindowClean 校验归一化与拒绝条件。
func TestTimeWindowClean(t *testing.T) {
	w := TimeWindow{From: "9:05", To: "8:00", Days: []int{5, 7, 5}, Zone: " Asia/Shanghai "}
	if err := w.clean(); err != nil {
		t.Fatalf("clean: %v", err)
	}
	if w.From != "09:05" || w.To != "08:00" {
		t.Fatalf("时分未归一化: %+v", w)
	}
	if len(w.Days) != 2 || w.Days[0] != 0 || w.Days[1] != 5 {
		t.Fatalf("星期未去重/归一到 0-6: %v", w.Days)
	}
	if w.Zone != "Asia/Shanghai" {
		t.Fatalf("时区未去空格: %q", w.Zone)
	}

	bad := []TimeWindow{
		{From: "24:00", To: "08:00"},
		{From: "09:00", To: "08:0"},
		{From: "09:00", To: "18:00", Days: []int{8}},
		{From: "09:00", To: "18:00", Zone: "Nowhere/Land"},
		{From: "00:00", To: "00:00"}, // 全天且不限星期 = 无条件
	}
	for _, w := range bad {
		if err := w.clean(); err == nil {
			t.Fatalf("非法时间窗应被拒绝: %+v", w)
		}
	}
}

// ---- effort 条件 ----

func TestEffortConditionHolds(t *testing.T) {
	cases := []struct {
		want, got string
		expect    bool
	}{
		{"", "", true},
		{"", "high", true},
		{"high", "high", true},
		{"high", "xhigh", true},
		{"high", "medium", false},
		{"high", "", false},
		{"on", "low", true},
		{"on", "", false},
		{"on", "none", false},
		{"medium", "High", true}, // 大小写不敏感
		{"medium", "minimal", false},
		{"bogus", "high", false}, // 写错的档位不猜
		{"high", "bogus", false},
	}
	for _, tc := range cases {
		if got := effortConditionHolds(tc.want, tc.got); got != tc.expect {
			t.Fatalf("effortConditionHolds(%q,%q) = %v, want %v", tc.want, tc.got, got, tc.expect)
		}
	}
}

// ---- 规则排序 ----

// TestApplyGroupRulesMovesUseFirst 命中后 Use 按列出顺序前移，其余保持原序，
// 且结果仍是原候选集的排列（fail-open 契约）。
func TestApplyGroupRulesMovesUseFirst(t *testing.T) {
	seats := ruleSeats()
	ctx := &RouteContext{Group: "g", EffortIn: "high"}
	rules := []GroupRule{{
		Use:    []string{"p-mid/m-mid", "p-cheap/m-cheap"},
		Effort: "high",
	}}

	got, hit := applyGroupRules(ctx, rules, seats)
	if hit != 1 {
		t.Fatalf("命中的规则序号 = %d, want 1", hit)
	}
	want := []string{"p-mid", "p-cheap", "p-strong"}
	if ids := providerIDsOf(got); !equalStrings(ids, want) {
		t.Fatalf("规则排序 = %v, want %v", ids, want)
	}
	if !sameMultiset(providerIDsOf(got), providerIDsOf(seats)) {
		t.Fatalf("规则结果必须是原候选的排列: %v", providerIDsOf(got))
	}
	if ctx.EffortOut != "" {
		t.Fatalf("没写 useEffort 时 EffortOut 应为空，得到 %q", ctx.EffortOut)
	}
}

// TestApplyGroupRulesNoMatchKeepsOrder 条件不成立时原序返回。
func TestApplyGroupRulesNoMatchKeepsOrder(t *testing.T) {
	seats := ruleSeats()
	ctx := &RouteContext{Group: "g", EffortIn: "low"}
	rules := []GroupRule{{Use: []string{"p-mid/m-mid"}, Effort: "high"}}

	got, hit := applyGroupRules(ctx, rules, seats)
	if hit != 0 {
		t.Fatalf("未命中时序号应为 0，得到 %d", hit)
	}
	if ids := providerIDsOf(got); !equalStrings(ids, providerIDsOf(seats)) {
		t.Fatalf("未命中应保持原序: %v", ids)
	}
}

// TestApplyGroupRulesFirstMatchWins 多条命中时取第一条。
func TestApplyGroupRulesFirstMatchWins(t *testing.T) {
	seats := ruleSeats()
	ctx := &RouteContext{Group: "g", EffortIn: "high"}
	rules := []GroupRule{
		{Use: []string{"p-cheap/m-cheap"}, Effort: "high"},
		{Use: []string{"p-mid/m-mid"}, Effort: "on"},
	}
	got, hit := applyGroupRules(ctx, rules, seats)
	if hit != 1 {
		t.Fatalf("应命中第一条，得到 %d", hit)
	}
	if got[0].ProviderID != "p-cheap" {
		t.Fatalf("第一条规则应生效，实际 %v", providerIDsOf(got))
	}
}

// TestApplyGroupRulesUnknownUseKeepsOrder 规则写了不存在的成员时不能引入候选。
func TestApplyGroupRulesUnknownUseKeepsOrder(t *testing.T) {
	seats := ruleSeats()
	ctx := &RouteContext{Group: "g", EffortIn: "high"}
	got, hit := applyGroupRules(ctx, []GroupRule{{Use: []string{"p-ghost/m"}, Effort: "high"}}, seats)
	if hit != 1 {
		t.Fatalf("规则本身命中，序号应为 1，得到 %d", hit)
	}
	if ids := providerIDsOf(got); !equalStrings(ids, providerIDsOf(seats)) {
		t.Fatalf("不认识的成员应被忽略，实际 %v", ids)
	}
}

// TestApplyGroupRulesWithoutConditionIgnored 没有条件的旧配置规则不能变成
// 「无条件改写顺序」。
func TestApplyGroupRulesWithoutConditionIgnored(t *testing.T) {
	seats := ruleSeats()
	ctx := &RouteContext{Group: "g", EffortIn: "high"}
	got, hit := applyGroupRules(ctx, []GroupRule{{Use: []string{"p-mid/m-mid"}}}, seats)
	if hit != 0 {
		t.Fatalf("无条件的规则不应命中，得到 %d", hit)
	}
	if ids := providerIDsOf(got); !equalStrings(ids, providerIDsOf(seats)) {
		t.Fatalf("原序应保持: %v", ids)
	}
}

// TestApplyGroupRulesUseEffort 命中规则把 UseEffort 写进 ctx（归一化）。
func TestApplyGroupRulesUseEffort(t *testing.T) {
	seats := ruleSeats()
	ctx := &RouteContext{Group: "g", EffortIn: "high"}
	rules := []GroupRule{{Use: []string{"p-strong/m-strong"}, Effort: "on", UseEffort: "High"}}
	if _, hit := applyGroupRules(ctx, rules, seats); hit != 1 {
		t.Fatalf("规则应命中，得到 %d", hit)
	}
	if ctx.EffortOut != "high" {
		t.Fatalf("EffortOut = %q, want high", ctx.EffortOut)
	}
}

// ---- intent 条件 ----

// TestApplyGroupRulesIntentMatches 分类器点名 p-cheap → intent=p-cheap 命中。
func TestApplyGroupRulesIntentMatches(t *testing.T) {
	seats := ruleSeats()
	calls := 0
	ctx := &RouteContext{
		Group: "g", Text: "帮我写一个排序算法",
		Intent: func(group, sessionKey string, seats []modelBinding, text string) int {
			calls++
			for i, b := range seats {
				if b.ProviderID == "p-cheap" {
					return i
				}
			}
			return 0
		},
	}
	// 两条 intent 规则共用一次分类调用。
	rules := []GroupRule{
		{Use: []string{"p-cheap/m-cheap"}, Intent: "p-cheap"},
		{Use: []string{"p-mid/m-mid"}, Intent: "p-mid"},
	}
	got, hit := applyGroupRules(ctx, rules, seats)
	if hit != 1 || got[0].ProviderID != "p-cheap" {
		t.Fatalf("intent 命中结果 = %v (hit=%d)", providerIDsOf(got), hit)
	}
	if calls != 1 {
		t.Fatalf("同一请求只应分类一次，实际 %d 次", calls)
	}
}

// TestApplyGroupRulesIntentFailOpen 分类器不可用 / 失败 / 指错候选时条件不成立。
func TestApplyGroupRulesIntentFailOpen(t *testing.T) {
	seats := ruleSeats()
	rules := []GroupRule{{Use: []string{"p-cheap/m-cheap"}, Intent: "p-cheap"}}

	// 未注入分类器（未配置 / 防递归路径）。
	got, hit := applyGroupRules(&RouteContext{Group: "g", Text: "写代码"}, rules, seats)
	if hit != 0 || !equalStrings(providerIDsOf(got), providerIDsOf(seats)) {
		t.Fatalf("无分类器时 intent 不应命中: %v (hit=%d)", providerIDsOf(got), hit)
	}

	// 分类失败（-1）。
	got, hit = applyGroupRules(&RouteContext{
		Group: "g", Text: "写代码",
		Intent: func(string, string, []modelBinding, string) int { return -1 },
	}, rules, seats)
	if hit != 0 || !equalStrings(providerIDsOf(got), providerIDsOf(seats)) {
		t.Fatalf("分类失败时 intent 不应命中: %v (hit=%d)", providerIDsOf(got), hit)
	}

	// 分类点名了别的候选 → intent 不匹配。
	got, hit = applyGroupRules(&RouteContext{
		Group: "g", Text: "写代码",
		Intent: func(string, string, []modelBinding, string) int { return 0 }, // p-strong
	}, rules, seats)
	if hit != 0 {
		t.Fatalf("点名 p-strong 时 intent=p-cheap 不应命中: %v", providerIDsOf(got))
	}
}

// ---- 规则配置校验 ----

func TestCleanGroupRules(t *testing.T) {
	members := []string{"mock-native/echo-pro", "mock-weak/echo-lite"}

	good := []GroupRule{{
		Use:       []string{"mock-weak/echo-lite", " mock-weak/echo-lite "},
		UseEffort: "XHigh",
		Effort:    "HIGH",
		Intent:    "  p-cheap   cheap  ",
		Time:      &TimeWindow{From: "9:00", To: "18:00", Days: []int{1}},
	}}
	cleaned, err := cleanGroupRules(good, members)
	if err != nil {
		t.Fatalf("cleanGroupRules: %v", err)
	}
	if len(cleaned) != 1 {
		t.Fatalf("规则数 = %d", len(cleaned))
	}
	r := cleaned[0]
	if len(r.Use) != 1 || r.Use[0] != "mock-weak/echo-lite" {
		t.Fatalf("use 未去重: %v", r.Use)
	}
	if r.UseEffort != "xhigh" || r.Effort != "high" {
		t.Fatalf("档位未归一化: %+v", r)
	}
	if r.Intent != "p-cheap cheap" {
		t.Fatalf("intent 未归一化空白: %q", r.Intent)
	}
	if r.Time == nil || r.Time.From != "09:00" {
		t.Fatalf("time 未归一化: %+v", r.Time)
	}

	bad := []struct {
		name  string
		rules []GroupRule
	}{
		{"use 为空", []GroupRule{{Effort: "high"}}},
		{"use 不在成员里", []GroupRule{{Use: []string{"ghost/x"}, Effort: "high"}}},
		{"use 指向嵌套组", []GroupRule{{Use: []string{"group/inner"}, Effort: "high"}}},
		{"useEffort 非法", []GroupRule{{Use: []string{"mock-native/echo-pro"}, UseEffort: "ultra", Effort: "high"}}},
		{"effort 非法", []GroupRule{{Use: []string{"mock-native/echo-pro"}, Effort: "maximal"}}},
		{"没有条件", []GroupRule{{Use: []string{"mock-native/echo-pro"}}}},
		{"时间窗非法", []GroupRule{{Use: []string{"mock-native/echo-pro"}, Time: &TimeWindow{From: "x", To: "18:00"}}}},
	}
	for _, tc := range bad {
		if _, err := cleanGroupRules(tc.rules, members); err == nil {
			t.Fatalf("%s：应被拒绝", tc.name)
		}
	}
}

// TestApplyGroupRulesTimeCondition 时间条件命中/不命中（时钟由 ctx.Now 注入）。
func TestApplyGroupRulesTimeCondition(t *testing.T) {
	seats := ruleSeats()
	window := &TimeWindow{From: "09:00", To: "18:00", Zone: "UTC"}
	rules := []GroupRule{{Use: []string{"p-mid/m-mid"}, Time: window}}

	inHours := &RouteContext{Group: "g", Now: time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)}
	got, hit := applyGroupRules(inHours, rules, seats)
	if hit != 1 || got[0].ProviderID != "p-mid" {
		t.Fatalf("时段内应命中并前移，实际 %v (hit=%d)", providerIDsOf(got), hit)
	}

	afterHours := &RouteContext{Group: "g", Now: time.Date(2026, 1, 2, 20, 0, 0, 0, time.UTC)}
	got, hit = applyGroupRules(afterHours, rules, seats)
	if hit != 0 || !equalStrings(providerIDsOf(got), providerIDsOf(seats)) {
		t.Fatalf("时段外不应命中，实际 %v (hit=%d)", providerIDsOf(got), hit)
	}
}

// ---- 集成：ResolveSmartEffort ----

func TestResolveSmartEffortAppliesRules(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroup("g", []string{"mock-native", "mock-weak"})
	r.SetVirtualGroupRules("g", []GroupRule{{
		Use:       []string{"mock-weak/echo-lite"},
		Effort:    "high",
		UseEffort: "max",
	}})

	// effort 不满足 → 原序，无输出。
	got, oc := r.ResolveSmartEffort("g", "写代码", "sk1", "low", false)
	if ids := providerIDsOf(got); !equalStrings(ids, []string{"mock-native", "mock-weak"}) {
		t.Fatalf("effort 不满足时应保持配置序: %v", ids)
	}
	if oc.RuleHit != 0 || oc.EffortOut != "" {
		t.Fatalf("未命中时 output = %+v", oc)
	}

	// effort 满足 → 规则成员前移，并带出 UseEffort。
	got, oc = r.ResolveSmartEffort("g", "写代码", "sk1", "high", false)
	if ids := providerIDsOf(got); !equalStrings(ids, []string{"mock-weak", "mock-native"}) {
		t.Fatalf("规则应把 use 提到链首: %v", ids)
	}
	if oc.RuleHit != 1 || oc.EffortOut != "max" {
		t.Fatalf("output = %+v, want RuleHit=1 EffortOut=max", oc)
	}

	// 普通 Resolve 不套规则（没有 effort 输入）。
	if ids := providerIDsOf(r.Resolve("g")); !equalStrings(ids, []string{"mock-native", "mock-weak"}) {
		t.Fatalf("Resolve 不应改顺序: %v", ids)
	}
}

// TestResolveSmartEffortIntentRule 端到端：intent 规则按注入的分类器命中。
func TestResolveSmartEffortIntentRule(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroup("g", []string{"mock-native", "mock-weak"})
	r.SetVirtualGroupRules("g", []GroupRule{{
		Use:    []string{"mock-weak/echo-lite"},
		Intent: "mock-weak",
	}})
	r.SetIntentClassifier(func(group, sessionKey string, seats []modelBinding, text string) int {
		for i, b := range seats {
			if b.ProviderID == "mock-weak" {
				return i
			}
		}
		return 0
	})

	got, oc := r.ResolveSmartEffort("g", "这段提问足够长，会真正触发分类器", "sk1", "", false)
	if ids := providerIDsOf(got); !equalStrings(ids, []string{"mock-weak", "mock-native"}) {
		t.Fatalf("intent 规则应命中并把成员前移: %v", ids)
	}
	if oc.RuleHit != 1 {
		t.Fatalf("RuleHit = %d", oc.RuleHit)
	}

	// 防递归路径（fromRouter=true）不注入分类器 → 规则不命中。
	got, oc = r.ResolveSmartEffort("g", "这段提问足够长，会真正触发分类器", "sk1", "", true)
	if ids := providerIDsOf(got); !equalStrings(ids, []string{"mock-native", "mock-weak"}) {
		t.Fatalf("防递归路径不应分类: %v", ids)
	}
	if oc.RuleHit != 0 {
		t.Fatalf("防递归路径不应命中 intent 规则: %+v", oc)
	}
}
