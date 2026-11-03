package mw

import "testing"

// 零值引擎（没载入任何规则）不能声称自己启用：
// 否则所有没配规则的部署都会被无谓地挡在 Fast-Path 之外。
func TestEngineEnabledZeroValue(t *testing.T) {
	var zero Engine
	if zero.Enabled() {
		t.Error("零值引擎不该认为有启用规则")
	}

	if New("").Enabled() {
		t.Error("空引擎不该认为有启用规则")
	}
}

// 未知 Kind 在 Apply 里会被直接跳过，所以不该影响直通判定。
func TestEngineEnabledUnknownKind(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{ID: "u1", Kind: Kind("no_such_kind"), Name: "未来版本的类型", Enabled: true})
	if e.Enabled() {
		t.Error("未知 Kind 不该算作启用规则")
	}
}

// 启用但还没填内容的规则仍然要挡：宁可少走直通，
// 也不能让"用户已经打开了开关"这件事被静默忽略。
func TestEngineEnabledEmptyRule(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{ID: "e1", Kind: KindParamOverride, Name: "还没配参数", Enabled: true})
	if !e.Enabled() {
		t.Error("启用中的空规则也必须挡住直通")
	}
}

// 关闭的规则和未知类型混在一起时，只有真正会执行的规则才算数。
func TestEngineEnabledIgnoresDisabled(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{ID: "d1", Kind: KindWordGuard, Name: "关掉的", Enabled: false, Words: []string{"x"}})
	_ = e.Upsert(Rule{ID: "d2", Kind: Kind("bogus"), Name: "未知", Enabled: true})
	if e.Enabled() {
		t.Error("关闭的规则 + 未知类型都不该算启用")
	}

	_ = e.Upsert(Rule{ID: "d3", Kind: KindSystemPrompt, Name: "开启的", Enabled: true, Prompt: "x"})
	if !e.Enabled() {
		t.Error("只要有一条启用规则就该返回 true")
	}
}
