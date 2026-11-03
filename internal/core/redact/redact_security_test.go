package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

// 占位符必须"同值同符、不同值不同符"，且不能是可离线反推的纯哈希。
func TestPlaceholderEntropyAndDeterminism(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	a1 := e.Mask("mail alice@example.com here", "", "")
	a2 := e.Mask("again alice@example.com here", "", "")
	ph1 := firstPlaceholder(t, a1.Text)
	ph2 := firstPlaceholder(t, a2.Text)
	if ph1 != ph2 {
		t.Fatalf("同值必须同符：%q vs %q", ph1, ph2)
	}

	b := e.Mask("mail bob@example.com here", "", "")
	if got := firstPlaceholder(t, b.Text); got == ph1 {
		t.Fatalf("不同值撞符：%q", got)
	}

	// 旧实现是 6 位 hex（24 bit）；这里锁住"至少 16 位"，
	// 防止有人为了好看把熵改回去。
	body := strings.TrimSuffix(strings.TrimPrefix(ph1, "{{EMAIL_"), "}}")
	if len(body) < 16 {
		t.Fatalf("占位符熵过低：%q（%d 位）", ph1, len(body))
	}

	// 不同引擎（不同盐）对同一个值必须给出不同占位符，
	// 否则占位符本身就是可离线穷举的指纹。
	e2 := New()
	e2.SetEnabled(true)
	if got := firstPlaceholder(t, e2.Mask("mail alice@example.com here", "", "").Text); got == ph1 {
		t.Fatalf("不同进程给出相同占位符，说明没加盐：%q", got)
	}
}

func firstPlaceholder(t *testing.T, s string) string {
	t.Helper()
	i := strings.Index(s, "{{")
	if i < 0 {
		t.Fatalf("没有占位符：%q", s)
	}
	j := strings.Index(s[i:], "}}")
	if j < 0 {
		t.Fatalf("占位符未闭合：%q", s)
	}
	return s[i : i+j+2]
}

// 回填进 JSON 语境时必须转义，否则原值里的引号/反斜杠会拼出非法 JSON。
func TestUnmaskJSONEscapesValue(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	raw := `p@ss"word\with\backslash`
	// 用自定义规则精确命中这个值：它不匹配任何内置形状，
	// 而这里要测的是"回填转义"，不是"规则能否发现它"。
	if err := e.SetCustomRules([]CustomRule{{Kind: "SECRET", Prefix: raw}}); err != nil {
		t.Fatalf("SetCustomRules: %v", err)
	}
	masked := e.Mask("k="+raw+" end", "", "").Text
	ph := firstPlaceholder(t, masked)
	if !strings.Contains(e.Unmask(ph), raw) {
		t.Fatal("普通还原应给出原值")
	}

	// 模拟工具参数：占位符落在 JSON 字符串字面量里。
	doc := `{"cmd":"` + ph + `"}`
	fixed := e.UnmaskJSON(doc)
	if !json.Valid([]byte(fixed)) {
		t.Fatalf("UnmaskJSON 之后不是合法 JSON：%q", fixed)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(fixed), &v); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if v["cmd"] != raw {
		t.Fatalf("还原值不对：%q != %q", v["cmd"], raw)
	}

	// 普通正文不能走 JSON 转义，否则引号会被错误地加上反斜杠。
	if got := e.Unmask(doc); !strings.Contains(got, raw) {
		t.Fatalf("Unmask 不该转义：%q", got)
	}
}

// 值映射必须有界：长时间运行不能只增不减。
func TestValueTablesAreBounded(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	// 直接构造超量值，避免跑 2 万次正则（慢）。
	e.mu.Lock()
	for i := 0; i < maxValues+100; i++ {
		e.placeholderFor("value-"+string(rune('a'+i%26))+string(rune(i)), "TERM")
	}
	n := len(e.forward)
	rev := len(e.reverse)
	e.mu.Unlock()

	if n > maxValues {
		t.Fatalf("forward 超过上限：%d > %d", n, maxValues)
	}
	if rev != n {
		t.Fatalf("forward/reverse 不一致：%d vs %d", n, rev)
	}
}

// 新增的口令/密钥规则：URL 内嵌密码、鉴权头、环境变量、JSON 字段、中文写法。
func TestSecretRulesCoverRealWorldShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // 必须被替换掉的子串
		kind string
	}{
		{"URL 内嵌密码", "postgres://app:hunter2pass@db.internal:5432/x", "hunter2pass", "PASSWORD"},
		{"Authorization 头", "curl -H 'Authorization: Bearer abcdefghijklmnop123456'", "abcdefghijklmnop123456", "SECRET"},
		{"x-api-key 头", `x-api-key: 9f8e7d6c5b4a3210`, "9f8e7d6c5b4a3210", "SECRET"},
		{"环境变量", "export DB_PASSWORD=hunter2pass", "hunter2pass", "SECRET"},
		{"JSON 字段", `{"client_secret":"s3cr3t-value-1234"}`, "s3cr3t-value-1234", "SECRET"},
		{"中文写法", "密码：hunter2pass", "hunter2pass", "PASSWORD"},
		{"火山方舟", "ark-1234abcd-5678efgh-9012ijkl", "ark-1234abcd-5678efgh-9012ijkl", "API_KEY"},
		{"OpenRouter", "sk-or-v1-0123456789abcdef0123456789abcdef", "sk-or-v1-0123456789abcdef0123456789abcdef", "API_KEY"},
		{"Groq", "gsk_0123456789abcdef0123456789abcdef01234567", "gsk_0123456789abcdef0123456789abcdef01234567", "API_KEY"},
		{"HuggingFace", "hf_0123456789abcdef0123456789abcdef", "hf_0123456789abcdef0123456789abcdef", "API_KEY"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			e.SetEnabled(true)
			r := e.Mask(tc.in, "", "")
			if strings.Contains(r.Text, tc.want) {
				t.Fatalf("未脱敏：%q → %q", tc.in, r.Text)
			}
			found := false
			for _, h := range r.Hits {
				if h.Kind == tc.kind {
					found = true
					break
				}
			}
			if !found {
				var kinds []string
				for _, h := range r.Hits {
					kinds = append(kinds, h.Kind)
				}
				t.Fatalf("命中类型里没有 %s：%v（文本=%q）", tc.kind, kinds, r.Text)
			}
		})
	}
}

// 代码里的普通标识符不能被当成口令脱敏——否则噪音会淹没真实命中。
func TestSecretRulesDoNotEatCode(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	for _, s := range []string{
		"const sort_key = cache",
		"cacheToken: 42",
		"func (e *Engine) placeholderFor(value, kind string)",
		"https://example.com:8443/api/v1",
	} {
		r := e.Mask(s, "", "")
		if strings.Contains(r.Text, "{{") {
			t.Errorf("误伤：%q → %q", s, r.Text)
		}
	}
}
