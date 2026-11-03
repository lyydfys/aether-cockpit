package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// 纯展开用的假渠道：渠道 ID 就是上游模型名，测试只关心顺序。
func fakeSeatSource(chains map[string][]string) groupChainSource {
	return groupChainSource{
		Chains: chains,
		DefaultSeat: func(pid string) (modelBinding, bool) {
			return modelBinding{ProviderID: pid, UpstreamModel: pid}, true
		},
	}
}

// TestNestedGroupExpandsInOrder 两层嵌套按外层声明顺序展开。
func TestNestedGroupExpandsInOrder(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroup("inner", []string{"mock-weak"})
	r.SetVirtualGroup("outer", []string{"mock-native", "group/inner"})

	got := r.Resolve("outer")
	want := []string{"mock-native", "mock-weak"}
	if gotIDs := providerIDsOf(got); !equalStrings(gotIDs, want) {
		t.Fatalf("嵌套展开顺序 = %v, want %v", gotIDs, want)
	}
	if got[1].UpstreamModel != "echo-lite" {
		t.Fatalf("内层组应取该渠道 manifest 的第一个模型，得到 %q", got[1].UpstreamModel)
	}

	// 内层组写在前面时，展开结果也应在前面。
	r.SetVirtualGroup("outer2", []string{"group/inner", "mock-native"})
	got = r.Resolve("outer2")
	if gotIDs := providerIDsOf(got); !equalStrings(gotIDs, []string{"mock-weak", "mock-native"}) {
		t.Fatalf("嵌套在前时顺序 = %v", gotIDs)
	}
}

// TestNestedGroupCycleKeepsOrder 成环不能死循环，也不能丢掉外层其余成员。
func TestNestedGroupCycleKeepsOrder(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroup("a", []string{"mock-native", "group/b"})
	r.SetVirtualGroup("b", []string{"group/a", "mock-weak"})

	got := r.Resolve("a")
	want := []string{"mock-native", "mock-weak"}
	if gotIDs := providerIDsOf(got); !equalStrings(gotIDs, want) {
		t.Fatalf("成环时顺序 = %v, want %v", gotIDs, want)
	}

	// 纯函数层面必须给出成环 warning，而不是静默吞掉。
	src := fakeSeatSource(map[string][]string{
		"a": {"p0", "group/b"},
		"b": {"group/a", "p1"},
	})
	seats, warns := expandGroupChain(src, "a")
	if ids := providerIDsOf(seats); !equalStrings(ids, []string{"p0", "p1"}) {
		t.Fatalf("成环展开 = %v", ids)
	}
	if !anyContains(warns, "成环") {
		t.Fatalf("成环必须记 warning，实际 %v", warns)
	}
}

// TestExpandGroupChainDepthLimit 超过 8 层截断，且不丢已展开的候选。
func TestExpandGroupChainDepthLimit(t *testing.T) {
	chains := make(map[string][]string, groupMaxNest+2)
	for i := 0; i < groupMaxNest+2; i++ {
		pid := fmt.Sprintf("p%d", i)
		if i == groupMaxNest+1 {
			chains[fmt.Sprintf("g%d", i)] = []string{pid}
			continue
		}
		chains[fmt.Sprintf("g%d", i)] = []string{pid, fmt.Sprintf("group/g%d", i+1)}
	}
	seats, warns := expandGroupChain(fakeSeatSource(chains), "g0")

	ids := providerIDsOf(seats)
	// 根 + 8 层嵌套内层各贡献一个候选，第 10 层被截断。
	if len(ids) != groupMaxNest+1 {
		t.Fatalf("深度截断后候选数 = %d (%v), want %d", len(ids), ids, groupMaxNest+1)
	}
	if ids[len(ids)-1] != fmt.Sprintf("p%d", groupMaxNest) {
		t.Fatalf("最后一个候选 = %q, want p%d", ids[len(ids)-1], groupMaxNest)
	}
	if anyContains(ids, fmt.Sprintf("p%d", groupMaxNest+1)) {
		t.Fatalf("超过深度的候选不应出现: %v", ids)
	}
	if !anyContains(warns, "层嵌套") {
		t.Fatalf("超深必须记 warning，实际 %v", warns)
	}
}

// TestExpandGroupChainMissingGroup 引用已删除/未注册的组时跳过并记 warning。
func TestExpandGroupChainMissingGroup(t *testing.T) {
	src := fakeSeatSource(map[string][]string{
		"a": {"p0", "group/ghost"},
	})
	seats, warns := expandGroupChain(src, "a")
	if ids := providerIDsOf(seats); !equalStrings(ids, []string{"p0"}) {
		t.Fatalf("缺失组应被跳过，实际 %v", ids)
	}
	if !anyContains(warns, "未注册") {
		t.Fatalf("缺失组必须记 warning，实际 %v", warns)
	}
}

// TestNestedGroupInheritsOuterStrategy 内层组没写 routing 时沿用外层策略：
// rotate 作用在整条展开链上（对齐 aether Group.Within）。
func TestNestedGroupInheritsOuterStrategy(t *testing.T) {
	r := newTestRouter()
	r.SetVirtualGroupWithStrategy("inner", []string{"mock-weak"}, GroupStrategyPriority)
	r.SetVirtualGroupWithStrategy("outer", []string{"group/inner", "mock-native"}, GroupStrategyRotate)

	first := providerIDsOf(r.Resolve("outer"))
	second := providerIDsOf(r.Resolve("outer"))
	if equalStrings(first, second) {
		t.Fatalf("外层 rotate 应作用于整条展开链，两次解析不应同序: %v / %v", first, second)
	}
	wantSet := []string{"mock-native", "mock-weak"}
	for _, round := range [][]string{first, second} {
		if !sameMultiset(round, wantSet) {
			t.Fatalf("rotate 结果必须是候选集的排列，实际 %v", round)
		}
	}
}

// TestNestedGroupWarnings 页面提示只对真问题报警。
func TestNestedGroupWarnings(t *testing.T) {
	known := map[string]bool{"inner": true}
	members := []string{"mock-native/echo-pro", "group/inner", "group/ghost", "group/self"}
	warns := nestedGroupWarnings(members, "self", known)
	if len(warns) != 2 {
		t.Fatalf("应只对不存在的组与自引用报警，实际 %v", warns)
	}
	if !anyContains(warns, "group/ghost") || !anyContains(warns, "组自身") {
		t.Fatalf("warning 内容不对: %v", warns)
	}
	if got := nestedGroupWarnings([]string{"group/inner"}, "outer", known); len(got) != 0 {
		t.Fatalf("正常嵌套不应报警: %v", got)
	}
}

// TestGroupsSaveNestedAndRules 端到端：保存嵌套组 + 规则 → 持久化 → 重启
// 后解析仍按嵌套顺序与规则生效。
func TestGroupsSaveNestedAndRules(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})

	groupAction(t, g, "save",
		`{"id":"inner","name":"Inner","members":["mock-weak/echo-lite"],"routing":"order"}`)
	out := groupAction(t, g, "save",
		`{"id":"outer","name":"Outer","members":["mock-native/echo-pro","group/inner"],"routing":"order",`+
			`"rules":[{"use":["mock-weak/echo-lite"],"effort":"high","useEffort":"xhigh"}]}`)

	grp := groupOf(out, "outer")
	if grp == nil {
		t.Fatalf("保存后的组缺失: %#v", out["groups"])
	}
	if warns := anyStrings(grp["warnings"]); len(warns) != 0 {
		t.Fatalf("合法嵌套 + 规则不应有 warning: %v", warns)
	}
	if _, ok := grp["rules"]; !ok {
		t.Fatal("rules 应回显给页面")
	}

	raw := g.settings.ModelGroupsOverride["outer"]
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("解析持久化配置: %v", err)
	}
	chain, _ := stored["chain"].([]any)
	if len(chain) != 2 || chain[1] != "group/inner" {
		t.Fatalf("chain 应保留嵌套引用，实际 %v", stored["chain"])
	}
	if _, ok := stored["rules"]; !ok {
		t.Fatal("rules 必须写回配置")
	}

	// 重启：同一份设置文件重建后，嵌套与规则都还在。
	g2 := New(Config{SettingsPath: g.settingsPath, UsagePath: "-"})
	got := providerIDsOf(g2.models.Resolve("outer"))
	if !equalStrings(got, []string{"mock-native", "mock-weak"}) {
		t.Fatalf("重启后嵌套顺序 = %v", got)
	}
	ranked, oc := g2.models.ResolveSmartEffort("outer", "写一段排序算法", "sk1", "high", false)
	if !equalStrings(providerIDsOf(ranked), []string{"mock-weak", "mock-native"}) {
		t.Fatalf("重启后规则应把 use 提到链首，实际 %v", providerIDsOf(ranked))
	}
	if oc.RuleHit != 1 || oc.EffortOut != "xhigh" {
		t.Fatalf("规则输出 = %+v, want RuleHit=1 EffortOut=xhigh", oc)
	}
}

// TestGroupsManualSkipsRules manual 组用 pick 指定唯一成员，规则只保存不生效
// （对齐 aether Group.Live），并且页面必须给出提示。
func TestGroupsManualSkipsRules(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	out := groupAction(t, g, "save",
		`{"id":"m","name":"M","members":["mock-native/echo-pro","mock-weak/echo-lite"],`+
			`"routing":"manual","pick":"mock-native/echo-pro",`+
			`"rules":[{"use":["mock-weak/echo-lite"],"effort":"high"}]}`)
	grp := groupOf(out, "m")
	if grp == nil {
		t.Fatalf("组缺失: %#v", out["groups"])
	}
	if warns := anyStrings(grp["warnings"]); !anyContains(warns, "rules 不生效") {
		t.Fatalf("manual 组带规则必须提示不生效: %v", warns)
	}

	ranked, oc := g.models.ResolveSmartEffort("m", "写代码", "sk1", "high", false)
	if ids := providerIDsOf(ranked); ids[0] != "mock-native" {
		t.Fatalf("manual 的 pick 应优先于规则: %v", ids)
	}
	if oc.RuleHit != 0 {
		t.Fatalf("manual 组不应套用规则，得到 %+v", oc)
	}
}

// TestGroupsPickRejectsNested 用 pick 指向嵌套组会变成「选了却没用」，必须拒绝。
func TestGroupsPickRejectsNested(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	groupAction(t, g, "save", `{"id":"inner","name":"Inner","members":["mock-weak/echo-lite"],"routing":"order"}`)
	groupAction(t, g, "save",
		`{"id":"outer","name":"Outer","members":["mock-native/echo-pro","group/inner"],"routing":"order"}`)

	code, body := groupActionRaw(t, g, "pick", `{"id":"outer","pick":"group/inner"}`)
	if code != 400 || !strings.Contains(body, "嵌套组") {
		t.Fatalf("pick 嵌套组应 400，得到 %d %s", code, body)
	}
}

// TestGroupsSaveRejectsBadNestedAndRules 嵌套组不存在 / 规则非法时保存失败，
// 不能存下一份永不生效的配置。
func TestGroupsSaveRejectsBadNestedAndRules(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "嵌套组不存在",
			body: `{"id":"x","name":"X","members":["mock-native/echo-pro","group/ghost"],"routing":"order"}`,
			want: "不存在",
		},
		{
			name: "规则成员不在组里",
			body: `{"id":"x","name":"X","members":["mock-native/echo-pro"],"routing":"order",` +
				`"rules":[{"use":["mock-weak/echo-lite"],"effort":"high"}]}`,
			want: "不是该组的成员",
		},
		{
			name: "规则没有条件",
			body: `{"id":"x","name":"X","members":["mock-native/echo-pro"],"routing":"order",` +
				`"rules":[{"use":["mock-native/echo-pro"]}]}`,
			want: "至少要有一个条件",
		},
		{
			name: "规则指向嵌套组",
			body: `{"id":"x","name":"X","members":["mock-native/echo-pro","group/ghost"],"routing":"order",` +
				`"rules":[{"use":["group/ghost"],"effort":"high"}]}`,
			want: "不存在",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := groupActionRaw(t, g, "save", tc.body)
			if code != 400 {
				t.Fatalf("应 400，得到 %d %s", code, body)
			}
			if !strings.Contains(body, tc.want) {
				t.Fatalf("错误文案应包含 %q，实际 %s", tc.want, body)
			}
			if _, ok := g.settings.ModelGroupsOverride["x"]; ok {
				t.Fatal("非法配置不应落盘")
			}
		})
	}
}

// groupActionRaw 返回原始状态码与响应体（错误路径断言用）。
func groupActionRaw(t *testing.T, g *Gateway, action, body string) (int, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/groups/{action}", g.handleGroupAction)
	req := httptest.NewRequest(http.MethodPost, "/api/groups/"+action, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
		if counts[s] < 0 {
			return false
		}
	}
	return true
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// anyStrings 把 JSON 反序列化后的 []any / []string 统一成 []string。
func anyStrings(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
