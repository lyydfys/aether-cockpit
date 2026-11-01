package redact

import (
	"regexp"
	"strings"
	"testing"
)

func TestMaskBasicKinds(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	cases := []struct {
		name string
		in   string
		kind string
	}{
		{"openai key", "here is my key sk-abcdefghijklmnopqrstuvwxyz123456 ok", "API_KEY"},
		{"aws key", "AKIAIOSFODNN7EXAMPLE", "API_KEY"},
		{"github token", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "API_KEY"},
		{"email", "联系我 zhangsan@example.com 谢谢", "EMAIL"},
		{"cn id", "身份证 110101199003071234 结束", "ID"},
		{"cn phone", "手机 13812345678 打给我", "PHONE"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := e.Mask(c.in, "p", "m")
			if len(r.Hits) == 0 {
				t.Fatalf("未命中任何规则: %q", c.in)
			}
			found := false
			for _, h := range r.Hits {
				if h.Kind == c.kind {
					found = true
				}
			}
			if !found {
				t.Errorf("期望命中 %s，实际 %+v", c.kind, r.Hits)
			}
			if r.Text == c.in {
				t.Error("文本未被改写")
			}
		})
	}
}

// 同一值必须永远是同一个占位符：上游按 prompt 内容做缓存，
// 占位符前后不一致会让缓存全部失效。
func TestSameValueSamePlaceholder(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	r1 := e.Mask("key: sk-abcdefghijklmnopqrstuvwxyz123456", "", "")
	r2 := e.Mask("again: sk-abcdefghijklmnopqrstuvwxyz123456", "", "")

	var p1, p2 string
	for _, h := range r1.Hits {
		if h.Placeholder != "" {
			p1 = h.Placeholder
		}
	}
	for _, h := range r2.Hits {
		if h.Placeholder != "" {
			p2 = h.Placeholder
		}
	}
	if p1 == "" || p1 != p2 {
		t.Fatalf("同值占位符不一致: %q vs %q", p1, p2)
	}
}

func TestDifferentValuesDifferentPlaceholders(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	r1 := e.Mask("sk-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "", "")
	r2 := e.Mask("sk-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "", "")

	p1 := r1.Hits[0].Placeholder
	p2 := r2.Hits[0].Placeholder
	if p1 == p2 {
		t.Fatalf("不同值应得到不同占位符: %q", p1)
	}
}

func TestUnmaskRoundTrip(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	const raw = "my key is sk-abcdefghijklmnopqrstuvwxyz123456"
	masked := e.Mask(raw, "", "").Text
	if masked == raw {
		t.Fatal("未脱敏")
	}
	back := e.Unmask(masked)
	if back != raw {
		t.Fatalf("还原失败:\n原: %q\n回: %q", raw, back)
	}
}

func TestDisabledPassThrough(t *testing.T) {
	e := New() // 默认关闭
	const raw = "sk-abcdefghijklmnopqrstuvwxyz123456"
	r := e.Mask(raw, "", "")
	if r.Text != raw {
		t.Error("关闭时不应改写文本")
	}
	if len(r.Hits) != 0 {
		t.Error("关闭时不应有命中")
	}
}

// 不能把正常内容误伤。
func TestNoFalsePositive(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	plain := []string{
		"这是一段普通的中文说明，没有任何敏感信息。",
		"func main() { fmt.Println(\"hello\") }",
		"模型名 claude-opus-4-6 和 gpt-5 都应该保留",
		"版本号 v1.2.3，端口 7860，路径 /api/state",
	}
	for _, p := range plain {
		r := e.Mask(p, "", "")
		if r.Text != p {
			t.Errorf("误伤: %q -> %q", p, r.Text)
		}
	}
}

func TestBlockAction(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	// 私钥是 block 之外最常见的 mask 目标；这里验证 block 分支的可达性
	// 通过直接构造一条 block 规则来测。
	e.rules = append(e.rules, Rule{
		ID: "test_block", Name: "测试拦截", Kind: "SECRET",
		Action: ActionBlock, Enabled: true,
		re: mustRe(`SECRET-DO-NOT-SEND`),
	})

	r := e.Mask("please carry SECRET-DO-NOT-SEND out", "", "")
	if !r.Blocked {
		t.Fatal("应被拦截")
	}
}

func TestPrivateKeyBlock(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	pem := `-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA1234567890abcdefghijklmnopqrstuvwxyz
abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMN
-----END RSA PRIVATE KEY-----`

	r := e.Mask(pem, "", "")
	if len(r.Hits) == 0 {
		t.Fatal("私钥未命中")
	}
	if strings.Contains(r.Text, "MIIEowIBAAKCAQEA") {
		t.Error("私钥内容仍在明文里")
	}
}

func TestSummaryCounts(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	e.Mask("sk-abcdefghijklmnopqrstuvwxyz123456 and a@b.com", "prov", "model")
	s := e.Summary(1)
	if s["requests"].(int) == 0 {
		t.Error("summary 应记录请求数")
	}
	if s["masked"].(int) == 0 {
		t.Error("summary 应记录脱敏次数")
	}
}

func TestClear(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	e.Mask("sk-abcdefghijklmnopqrstuvwxyz123456", "", "")
	if len(e.Records()) == 0 {
		t.Fatal("应有记录")
	}
	e.Clear()
	if len(e.Records()) != 0 {
		t.Error("clear 后不应有记录")
	}
	// 清空后同值会得到新的占位符，但仍应可逆
	masked := e.Mask("sk-abcdefghijklmnopqrstuvwxyz123456", "", "").Text
	if e.Unmask(masked) != "sk-abcdefghijklmnopqrstuvwxyz123456" {
		t.Error("清空后仍应可逆")
	}
}

func mustRe(s string) *regexp.Regexp { return regexp.MustCompile(s) }

func TestPersonalToggle(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	in := "mail a@b.com key sk-abcdefghijklmnopqrstuvwxyz123456"
	e.SetPersonal(false)
	r := e.Mask(in, "", "")
	if strings.Contains(r.Text, "{{EMAIL") {
		t.Fatalf("关闭个人信息脱敏后不应替换邮箱: %q", r.Text)
	}
	if !strings.Contains(r.Text, "{{API_KEY") {
		t.Fatalf("密钥仍应脱敏: %q", r.Text)
	}
	e.SetPersonal(true)
	if r := e.Mask(in, "", ""); !strings.Contains(r.Text, "{{EMAIL") {
		t.Fatalf("打开后应脱敏邮箱: %q", r.Text)
	}
}

func TestCustomRulesPrefixAndRegex(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	if err := e.SetCustomRules([]CustomRule{
		{Kind: "gateway key", Prefix: "oc_sk_"},
		{Kind: "badge", Regex: `ZW-[A-Z0-9]{6}`},
	}); err != nil {
		t.Fatalf("规则应能编译: %v", err)
	}

	r := e.Mask("use oc_sk_abc123XYZ and ZW-4F7K2Q now", "p", "m")
	if len(r.Hits) != 2 {
		t.Fatalf("应有 2 处命中: %+v", r.Hits)
	}
	if !strings.Contains(r.Text, "{{GATEWAY_KEY_") || !strings.Contains(r.Text, "{{BADGE_") {
		t.Fatalf("占位符类型不对: %q", r.Text)
	}
	if strings.Contains(r.Text, "abc123XYZ") || strings.Contains(r.Text, "ZW-4F7K2Q") {
		t.Fatalf("原值仍留在文本里: %q", r.Text)
	}
	// 前缀规则把同一 token 的剩余字符一起吃掉。
	if strings.Contains(r.Text, "abc123") {
		t.Fatalf("前缀规则只吃掉了前缀: %q", r.Text)
	}
	// 占位符可逆。
	back := e.Unmask(r.Text)
	if !strings.Contains(back, "oc_sk_abc123XYZ") || !strings.Contains(back, "ZW-4F7K2Q") {
		t.Fatalf("还原失败: %q", back)
	}
	// 用户规则同样计入动作覆盖表。
	e.SetActions(map[string]Action{"BADGE": ActionWarn})
	r = e.Mask("ZW-4F7K2Q", "p", "m")
	if len(r.Hits) != 1 || r.Hits[0].Action != ActionWarn || r.Text != "ZW-4F7K2Q" {
		t.Fatalf("BADGE 未按 warn 处理: %+v %q", r.Hits, r.Text)
	}
}

func TestCustomRulesRejectKeepsOld(t *testing.T) {
	e := New()
	if err := e.SetCustomRules([]CustomRule{{Kind: "k", Prefix: "oc_"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetCustomRules([]CustomRule{{Kind: "k", Regex: "("}}); err == nil {
		t.Fatal("坏正则应被拒绝")
	}
	if got := e.CustomRules(); len(got) != 1 || got[0].Prefix != "oc_" {
		t.Fatalf("拒绝后旧规则应保留: %+v", got)
	}
	if err := e.SetCustomRules([]CustomRule{{Kind: "k"}}); err == nil {
		t.Fatal("缺少匹配内容应被拒绝")
	}
	// 空列表表示全部删除。
	if err := e.SetCustomRules(nil); err != nil {
		t.Fatal(err)
	}
	if got := e.CustomRules(); len(got) != 0 {
		t.Fatalf("清空失败: %+v", got)
	}
}

func TestAllowListWholeValueAnyCase(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	e.SetAllow([]string{"ops@example.com", "sk-teamsharedkey0123456789"})

	r := e.Mask("mail OPS@Example.COM and ops@example.com, other dev@example.com", "p", "m")
	if !strings.Contains(r.Text, "OPS@Example.COM") {
		t.Fatalf("白名单值被改动: %q", r.Text)
	}
	// 另一个邮箱仍被脱敏。
	if strings.Contains(r.Text, "dev@example.com") {
		t.Fatalf("非白名单值未脱敏: %q", r.Text)
	}
	if len(r.Hits) != 1 || r.Hits[0].Kind != "EMAIL" {
		t.Fatalf("白名单命中不应记录: %+v", r.Hits)
	}
	// 整值比较：只是包含白名单片段的值仍要脱敏。
	r = e.Mask("sk-teamsharedkey0123456789x", "p", "m")
	if len(r.Hits) == 0 {
		t.Fatal("非整值匹配不应被白名单放过")
	}
}

func TestNormalizeKind(t *testing.T) {
	cases := map[string]string{
		"api key": "API_KEY",
		"  Token ": "TOKEN",
		"acme.host-name": "ACME_HOST_NAME",
		"":               "CUSTOM",
		"__":             "CUSTOM",
		"密钥":             "CUSTOM",
	}
	for in, want := range cases {
		if got := NormalizeKind(in); got != want {
			t.Errorf("NormalizeKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWordsBoundaryAndCase(t *testing.T) {
	e := New()
	e.SetEnabled(true)
	e.SetWords([]string{"Nightjar", "acme-corp"}, false)

	r := e.Mask("Nightjar and nightjar, Nightjars, acme-corp", "p", "m")
	if !strings.Contains(r.Text, "{{TERM_") {
		t.Fatalf("脱敏词未生效: %q", r.Text)
	}
	if strings.Count(r.Text, "{{TERM_") != 2 {
		t.Fatalf("应替换 2 处（Nightjar + acme-corp）: %q", r.Text)
	}
	if !strings.Contains(r.Text, " nightjar,") || !strings.Contains(r.Text, "Nightjars") {
		t.Fatalf("大小写/词内不应命中: %q", r.Text)
	}
	if back := e.Unmask(r.Text); back != "Nightjar and nightjar, Nightjars, acme-corp" {
		t.Fatalf("还原失败: %q", back)
	}

	e.SetWords([]string{"Nightjar"}, true)
	if r := e.Mask("NIGHTJAR and nightjar", "p", "m"); strings.Count(r.Text, "{{TERM_") != 2 {
		t.Fatalf("不区分大小写应替换 2 处: %q", r.Text)
	}
	if got := e.Words(); len(got) != 1 || got[0] != "Nightjar" {
		t.Fatalf("Words = %v", got)
	}
}

func TestPersonalDetailRules(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	cases := []struct{ in, kind, want string }{
		{"ssn 078-05-1120 ok", "SSN", "ssn {{SSN_"},
		{"my passport AB1234567 here", "PASSPORT", "my passport {{PASSPORT_"},
		{"iban GB82WEST12345698765432 ok", "IBAN", "iban {{IBAN_"},
		{"born 1990-05-17 in Rome", "BIRTHDAY", "born {{BIRTHDAY_"},
		{"mac aa:bb:cc:dd:ee:ff ok", "MAC", "mac {{MAC_"},
		{"S/N A1B2C3D4 ok", "SERIAL", "S/N {{SERIAL_"},
	}
	for _, c := range cases {
		r := e.Mask(c.in, "p", "m")
		found := false
		for _, h := range r.Hits {
			if h.Kind == c.kind {
				found = true
			}
		}
		if !found {
			t.Errorf("%q 应命中 %s: %+v", c.in, c.kind, r.Hits)
			continue
		}
		if !strings.Contains(r.Text, c.want) {
			t.Errorf("%q → %q，期望包含 %q", c.in, r.Text, c.want)
		}
	}

	// 关键词不能被一起吃进占位符。
	r := e.Mask("my passport AB1234567 here", "p", "m")
	if !strings.Contains(r.Text, "my passport ") || !strings.Contains(r.Text, " here") {
		t.Fatalf("只应替换号码: %q", r.Text)
	}
	// IBAN 校验和不对就不算。
	if r := e.Mask("GB82WEST12345698765431", "p", "m"); len(r.Hits) != 0 {
		t.Fatalf("坏校验和不应命中: %+v", r.Hits)
	}
	// "serial number" 是词不是序列号。
	if r := e.Mask("serial number please", "p", "m"); len(r.Hits) != 0 {
		t.Fatalf("serial number 不应命中: %+v", r.Hits)
	}
}

func TestKindToggles(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	// 主目录与桶默认关。
	if r := e.Mask("/Users/alice/project", "p", "m"); len(r.Hits) != 0 {
		t.Fatalf("HOME 默认应关: %+v", r.Hits)
	}
	if r := e.Mask("s3://acme-bucket/path", "p", "m"); len(r.Hits) != 0 {
		t.Fatalf("BUCKET 默认应关: %+v", r.Hits)
	}
	e.SetKindEnabled(map[string]bool{"HOME": true, "BUCKET": true})
	r := e.Mask("/Users/alice/project and s3://acme-bucket/path", "p", "m")
	if len(r.Hits) != 2 {
		t.Fatalf("开启后应命中 2 处: %+v", r.Hits)
	}
	if !strings.Contains(r.Text, "/Users/{{HOME_") || !strings.Contains(r.Text, "s3://{{BUCKET_") {
		t.Fatalf("只应替换用户名/桶名: %q", r.Text)
	}

	// 细项可单独关；主开关关掉后全部个人信息不脱敏。
	e.SetKindEnabled(map[string]bool{"MAC": false})
	if r := e.Mask("aa:bb:cc:dd:ee:ff", "p", "m"); len(r.Hits) != 0 {
		t.Fatalf("MAC 关掉后不应命中: %+v", r.Hits)
	}
	e.SetPersonal(false)
	if r := e.Mask("078-05-1120 aa:bb:cc:dd:ee:ff /Users/alice", "p", "m"); len(r.Hits) != 0 {
		t.Fatalf("个人信息总开关关闭后不应命中: %+v", r.Hits)
	}
	// 密钥类不受个人信息开关影响。
	if r := e.Mask("sk-abcdefghijklmnopqrstuvwxyz123456", "p", "m"); len(r.Hits) != 1 {
		t.Fatalf("API Key 应仍然脱敏: %+v", r.Hits)
	}
}

func TestUserhostAndPublicIP(t *testing.T) {
	e := New()
	e.SetEnabled(true)

	// 两项默认关。
	if r := e.Mask("alice@build-01 and 8.8.8.8", "p", "m"); len(r.Hits) != 0 {
		t.Fatalf("USERHOST/IP 默认应关: %+v", r.Hits)
	}
	e.SetKindEnabled(map[string]bool{"USERHOST": true, "IP_ADDR": true})

	r := e.Mask("alice@build-01 ssh and 8.8.8.8, 10.0.0.5, 192.168.1.2", "p", "m")
	kinds := map[string]int{}
	for _, h := range r.Hits {
		kinds[h.Kind]++
	}
	if kinds["USERHOST"] != 1 {
		t.Fatalf("user@host 应命中一次: %+v", r.Hits)
	}
	if kinds["IP_ADDR"] != 1 {
		t.Fatalf("只有公网 IP 应命中: %+v", r.Hits)
	}
	if !strings.Contains(r.Text, "{{USERHOST_") || !strings.Contains(r.Text, "{{IP_ADDR_") {
		t.Fatalf("占位符不对: %q", r.Text)
	}
	if !strings.Contains(r.Text, "10.0.0.5") || !strings.Contains(r.Text, "192.168.1.2") {
		t.Fatalf("私网 IP 不应被替换: %q", r.Text)
	}
	// 邮箱仍走 EMAIL，不会被 userhost 抢走。
	r = e.Mask("mail dev@example.com", "p", "m")
	if len(r.Hits) != 1 || r.Hits[0].Kind != "EMAIL" {
		t.Fatalf("邮箱应仍记 EMAIL: %+v", r.Hits)
	}
	// 文档段与回环不算公网。
	if r := e.Mask("203.0.113.9 127.0.0.1 169.254.1.1", "p", "m"); len(r.Hits) != 0 {
		t.Fatalf("保留段不应命中: %+v", r.Hits)
	}
	// 非法段值不命中。
	if r := e.Mask("999.1.1.1", "p", "m"); len(r.Hits) != 0 {
		t.Fatalf("非法 octet 不应命中: %+v", r.Hits)
	}
}

func TestKeepKeyShape(t *testing.T) {
	const raw = "sk-abcdefghijklmnopqrstuvwxyz123456"
	e := New()
	e.SetEnabled(true)
	e.SetShape(true)

	r := e.Mask("key "+raw+" here", "p", "m")
	if strings.Contains(r.Text, raw) || strings.Contains(r.Text, "{{API_KEY_") {
		t.Fatalf("同形模式不应出现原值或占位符: %q", r.Text)
	}
	// 同形：sk- 前缀保留、长度一致、字母数字。
	shaped := strings.TrimSuffix(strings.TrimPrefix(r.Text, "key "), " here")
	if !strings.HasPrefix(shaped, "sk-") || len([]rune(shaped)) != len([]rune(raw)) {
		t.Fatalf("形状没保留: %q", shaped)
	}
	for _, c := range shaped[3:] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			t.Fatalf("假值含非字母数字: %q", shaped)
		}
	}
	// 同值同形、可还原。
	r2 := e.Mask(raw, "p", "m")
	if r2.Text != shaped {
		t.Fatalf("同值应同形: %q vs %q", r2.Text, shaped)
	}
	if back := e.Unmask("echo " + shaped); back != "echo "+raw {
		t.Fatalf("假值未还原: %q", back)
	}
	// 记录里仍是占位符（脱敏页按类型聚合）。
	if len(r.Hits) != 1 || !strings.HasPrefix(r.Hits[0].Placeholder, "{{API_KEY_") {
		t.Fatalf("记录的占位符不对: %+v", r.Hits)
	}
	// 个人信息不走同形。
	if r := e.Mask("mail dev@example.com", "p", "m"); !strings.Contains(r.Text, "{{EMAIL_") {
		t.Fatalf("邮箱应仍是占位符: %q", r.Text)
	}
	// 关掉就回到占位符。
	e.SetShape(false)
	if r := e.Mask(raw, "p", "m"); !strings.Contains(r.Text, "{{API_KEY_") {
		t.Fatalf("关掉后应回到占位符: %q", r.Text)
	}
}
