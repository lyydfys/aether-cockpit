package gateway

import "testing"

// 组规则的条件：agents / folders / exceptFolders / tokens / images / pause。
//
// 这些条件都是"有值才判"：配置里没写就是通配，写了就必须全部成立。
func TestGroupRuleSignalConditions(t *testing.T) {
	seats := ruleSeats()

	base := func() *RouteContext {
		return &RouteContext{Group: "g", Tokens: 8000, Images: true, Agent: "claude_code", Folder: "/work/app"}
	}

	cases := []struct {
		name string
		rule GroupRule
		ctx  *RouteContext
		want bool
	}{
		{"agents 命中", GroupRule{Agents: []string{"claude_code"}, Use: []string{"p-mid/m-mid"}}, base(), true},
		{"agents 大小写无关", GroupRule{Agents: []string{"Claude_Code"}, Use: []string{"p-mid/m-mid"}}, base(), true},
		{"agents 不命中", GroupRule{Agents: []string{"codex_cli"}, Use: []string{"p-mid/m-mid"}}, base(), false},
		{"tokens 达到阈值", GroupRule{Tokens: 8000, Use: []string{"p-mid/m-mid"}}, base(), true},
		{"tokens 未达阈值", GroupRule{Tokens: 20000, Use: []string{"p-mid/m-mid"}}, base(), false},
		{"images 要求带图", GroupRule{Images: boolPtr(true), Use: []string{"p-mid/m-mid"}}, base(), true},
		{"images 要求不带图", GroupRule{Images: boolPtr(false), Use: []string{"p-mid/m-mid"}}, base(), false},
		{"folders 子目录命中", GroupRule{Folders: []string{"/work"}, Use: []string{"p-mid/m-mid"}}, base(), true},
		{"folders 前缀不能误判", GroupRule{Folders: []string{"/work/ap"}, Use: []string{"p-mid/m-mid"}}, base(), false},
		{"exceptFolders 排除", GroupRule{ExceptFolders: []string{"/work"}, Use: []string{"p-mid/m-mid"}}, base(), false},
		{"exceptFolders 未命中则成立", GroupRule{ExceptFolders: []string{"/other"}, Use: []string{"p-mid/m-mid"}}, base(), true},
		{"pause 停用", GroupRule{Pause: true, Agents: []string{"claude_code"}, Use: []string{"p-mid/m-mid"}}, base(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, hit := applyGroupRules(tc.ctx, []GroupRule{tc.rule}, seats)
			if got := hit > 0; got != tc.want {
				t.Fatalf("命中 = %v, want %v", got, tc.want)
			}
		})
	}
}

// 没有信号时（零值 RouteContext）带条件的规则一律不成立——
// 宁可规则不生效，也不能用错误的信号去命中。
func TestGroupRuleSignalsZeroValueDoesNotMatch(t *testing.T) {
	seats := ruleSeats()
	ctx := &RouteContext{Group: "g"}
	for _, rule := range []GroupRule{
		{Agents: []string{"claude_code"}, Use: []string{"p-mid/m-mid"}},
		{Tokens: 1, Use: []string{"p-mid/m-mid"}},
		{Folders: []string{"/work"}, Use: []string{"p-mid/m-mid"}},
	} {
		if _, hit := applyGroupRules(ctx, []GroupRule{rule}, seats); hit != 0 {
			t.Fatalf("零值信号不应命中：%+v", rule)
		}
	}
}

// 新条件必须和旧条件（effort/time/intent）一样保持"排列"契约。
func TestGroupRuleSignalConditionsKeepPermutation(t *testing.T) {
	seats := ruleSeats()
	ctx := &RouteContext{Group: "g", Agent: "claude_code", Tokens: 9000, Folder: "/work/app/a"}
	rules := []GroupRule{{
		Agents:  []string{"claude_code"},
		Tokens:  5000,
		Folders: []string{"/work"},
		Use:     []string{"p-cheap/m-cheap"},
	}}
	got, hit := applyGroupRules(ctx, rules, seats)
	if hit != 1 {
		t.Fatalf("规则应命中，得到 %d", hit)
	}
	// 规则会重排，比较的是"集合相同"而不是"顺序相同"。
	if !sameSet(providerIDsOf(got), providerIDsOf(seats)) {
		t.Fatalf("结果必须是候选集的排列：%v vs %v", providerIDsOf(got), providerIDsOf(seats))
	}
}

// sameSet 比较两个切片是否含相同元素（忽略顺序）。
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	count := map[string]int{}
	for _, v := range a {
		count[v]++
	}
	for _, v := range b {
		count[v]--
		if count[v] < 0 {
			return false
		}
	}
	return true
}

func boolPtr(v bool) *bool { return &v }
