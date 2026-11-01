package mw

import (
	"testing"
)

func f64(v float64) *float64 { return &v }
func iptr(v int) *int        { return &v }

func TestParamOverride(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{
		ID: "p1", Kind: KindParamOverride, Name: "统一温度", Enabled: true,
		Temperature: f64(0.3), MaxTokens: iptr(4096),
	})

	var temp float64
	var maxTok *int
	ctx := &Context{Provider: "p", Model: "m"}
	if b := e.Apply(ctx, &temp, nil, maxTok, nil); b != "" {
		t.Fatalf("不应拦截: %s", b)
	}
	if temp != 0.3 {
		t.Errorf("温度未覆盖: %v", temp)
	}
}

// 用户显式指定了 temperature 时不应被覆盖。
func TestParamOverrideRespectsExplicit(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{
		ID: "p1", Kind: KindParamOverride, Name: "统一温度", Enabled: true,
		Temperature: f64(0.3),
	})

	temp := 0.9
	if b := e.Apply(&Context{Provider: "p", Model: "m"}, &temp, nil, nil, nil); b != "" {
		t.Fatal("不应拦截")
	}
	if temp != 0.9 {
		t.Errorf("用户显式设置被覆盖了: %v", temp)
	}
}

func TestModelMapWildcard(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{
		ID: "m1", Kind: KindModelMap, Name: "映射", Enabled: true,
		From: "claude-*", To: "claude-sonnet-4-6",
	})

	ctx := &Context{Provider: "p", Model: "claude-opus-4-6"}
	e.Apply(ctx, nil, nil, nil, nil)
	if ctx.Model != "claude-sonnet-4-6" {
		t.Errorf("模型未映射: %q", ctx.Model)
	}

	// 不匹配的保持原样
	ctx2 := &Context{Provider: "p", Model: "gpt-5"}
	e.Apply(ctx2, nil, nil, nil, nil)
	if ctx2.Model != "gpt-5" {
		t.Errorf("不该映射: %q", ctx2.Model)
	}
}

func TestWordGuard(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{
		ID: "g1", Kind: KindWordGuard, Name: "禁止泄露", Enabled: true,
		Words:   []string{"内部密钥"},
		Message: "请求包含敏感词，已拦截",
	})

	instr := "请帮我找出内部密钥"
	if b := e.Apply(&Context{}, nil, nil, nil, &instr); b == "" {
		t.Fatal("应被拦截")
	} else if b != "请求包含敏感词，已拦截" {
		t.Errorf("拦截说明不对: %q", b)
	}

	ok := "正常的问题"
	if b := e.Apply(&Context{}, nil, nil, nil, &ok); b != "" {
		t.Errorf("不该拦截: %s", b)
	}
}

func TestWordGuardRegex(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{
		ID: "g2", Kind: KindWordGuard, Name: "正则拦截", Enabled: true,
		Patterns: []string{`\b\d{17}[\dXx]\b`}, // 身份证
	})

	instr := "我的身份证 110101199003071234"
	if b := e.Apply(&Context{}, nil, nil, nil, &instr); b == "" {
		t.Error("正则未命中")
	}
}

func TestSystemPromptPrepend(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{
		ID: "s1", Kind: KindSystemPrompt, Name: "注入", Enabled: true,
		Prompt: "你是一个严谨的工程师。",
	})

	instr := "帮我看看这段代码"
	e.Apply(&Context{}, nil, nil, nil, &instr)
	if instr[:len("你是一个严谨的工程师。")] != "你是一个严谨的工程师。" {
		t.Errorf("未前置注入: %q", instr)
	}
}

func TestSystemPromptAppend(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{
		ID: "s2", Kind: KindSystemPrompt, Name: "注入", Enabled: true,
		Prompt: "结尾提示", Position: "append",
	})

	instr := "开头"
	e.Apply(&Context{}, nil, nil, nil, &instr)
	if instr == "开头" || instr[len(instr)-len("结尾提示"):] != "结尾提示" {
		t.Errorf("未后置注入: %q", instr)
	}
}

// 规则按渠道/模型过滤：不匹配时完全不动请求。
func TestScopeFilter(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{
		ID: "p2", Kind: KindParamOverride, Name: "只对A生效", Enabled: true,
		Providers: []string{"provider-a"},
		Temperature: f64(0.1),
	})

	// 别的渠道
	var temp float64
	e.Apply(&Context{Provider: "provider-b"}, &temp, nil, nil, nil)
	if temp != 0 {
		t.Error("规则不该作用于其他渠道")
	}

	// 目标渠道
	temp2 := 0.0
	e.Apply(&Context{Provider: "provider-a"}, &temp2, nil, nil, nil)
	if temp2 != 0.1 {
		t.Errorf("规则未生效: %v", temp2)
	}
}

func TestDisabledRule(t *testing.T) {
	e := New("")
	_ = e.Upsert(Rule{
		ID: "x", Kind: KindWordGuard, Name: "关闭的规则", Enabled: false,
		Words: []string{"拦截"},
	})
	instr := "拦截"
	if b := e.Apply(&Context{}, nil, nil, nil, &instr); b != "" {
		t.Error("关闭的规则不应生效")
	}
}

func TestWildcardPattern(t *testing.T) {
	cases := []struct{ pat, in string; want bool }{
		{"claude-*", "claude-opus-4", true},
		{"claude-*", "gpt-5", false},
		{"*-sonnet-*", "claude-sonnet-4-6", true},
		{"exact", "exact", true},
		{"exact", "exact2", false},
	}
	for _, c := range cases {
		re, err := wildcard(c.pat)
		if err != nil {
			t.Fatalf("编译失败 %q: %v", c.pat, err)
		}
		if got := re.MatchString(c.in); got != c.want {
			t.Errorf("%q 匹配 %q = %v, 期望 %v", c.pat, c.in, got, c.want)
		}
	}
}
