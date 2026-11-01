package gateway

import (
	"context"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
)

func testCtx() context.Context { return context.Background() }

// placeholderFor 按原文反查占位符（占位符由 HMAC 派生，测试不能假设其取值）。
func placeholderFor(mapping map[string]string, secret string) (string, bool) {
	for ph, sec := range mapping {
		if sec == secret {
			return ph, true
		}
	}
	return "", false
}

func newTestEngine(t *testing.T, cfg PrivacyMaskConfig) *privacyEngine {
	t.Helper()
	e, err := newPrivacyEngine(cfg)
	if err != nil {
		t.Fatalf("newPrivacyEngine: %v", err)
	}
	return e
}

func TestPrivacyMaskRequest(t *testing.T) {
	e := newTestEngine(t, PrivacyMaskConfig{Enabled: true, Rules: []string{"keys", "jwt", "emails"}})

	req := &ir.UnifiedChatRequest{
		Instructions: "你是助手",
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "我的 key 是 sk-abcdefghij0123456789，邮箱 me@example.com"},
			{Type: ir.ItemTypeMessage, Role: "assistant", Text: "好的"},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "再发一次：sk-abcdefghij0123456789"},
		},
	}
	u, mErr := e.maskRequest(req)
	if mErr != nil {
		t.Fatalf("maskRequest: %v", mErr)
	}
	if u == nil {
		t.Fatal("预期有可遮蔽内容")
	}

	if strings.Contains(req.Items[0].Text, "sk-abcdefghij") {
		t.Errorf("key 未被遮蔽: %s", req.Items[0].Text)
	}
	if strings.Contains(req.Items[0].Text, "me@example.com") {
		t.Errorf("邮箱未被遮蔽: %s", req.Items[0].Text)
	}
	if !strings.Contains(req.Items[0].Text, placeholderPrefix) {
		t.Errorf("缺少占位符: %s", req.Items[0].Text)
	}

	// 同一敏感串在两轮里必须映射到同一占位符（保持上游看到的上下文一致）。
	// 注意：必须按"原文"挑占位符——map 迭代顺序随机，随便挑一个会取到邮箱的占位符。
	ph, ok := placeholderFor(u.mapping, "sk-abcdefghij0123456789")
	if !ok {
		t.Fatalf("映射里找不到 key 的占位符: %v", u.mapping)
	}
	if !strings.Contains(req.Items[0].Text, ph) || !strings.Contains(req.Items[2].Text, ph) {
		t.Errorf("占位符不一致: %q vs %q", req.Items[0].Text, req.Items[2].Text)
	}

	// 回复里出现占位符 → 还原成原文。
	got := u.push("已收到 ")
	got += u.push(ph)
	got += u.flush()
	if !strings.Contains(got, "sk-abcdefghij0123456789") {
		t.Errorf("占位符未还原: %q", got)
	}
}

func TestPrivacyUnmaskSplitPlaceholder(t *testing.T) {
	e := newTestEngine(t, PrivacyMaskConfig{Enabled: true, Rules: []string{"keys"}})
	req := &ir.UnifiedChatRequest{
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "token=ghp_abcdefghijklmnopqrstuvwxyz0123456789"}},
	}
	u, mErr := e.maskRequest(req)
	if mErr != nil {
		t.Fatalf("maskRequest: %v", mErr)
	}
	if u == nil {
		t.Fatal("预期有可遮蔽内容")
	}
	var ph string
	for ph = range u.mapping {
		break
	}
	if ph == "" {
		t.Fatal("映射为空")
	}

	// 把占位符切成 3 段模拟流式分片。
	out := u.push("前缀 " + ph[:5])
	if strings.Contains(out, "__AE_") {
		t.Errorf("占位符前缀被提前下发: %q", out)
	}
	out += u.push(ph[5:])
	if !strings.Contains(out, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") {
		t.Errorf("分片占位符未还原: %q", out)
	}
	out += u.flush()
	if strings.Contains(out, placeholderPrefix) {
		t.Errorf("flush 后仍有占位符残留: %q", out)
	}
}

func TestPrivacyWrapStreamsFlushBeforeDone(t *testing.T) {
	e := newTestEngine(t, PrivacyMaskConfig{Enabled: true, Rules: []string{"keys"}})
	req := &ir.UnifiedChatRequest{
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "k=sk-abcdefghij0123456789"}},
	}
	u, mErr := e.maskRequest(req)
	if mErr != nil {
		t.Fatalf("maskRequest: %v", mErr)
	}

	var ph string
	for ph = range u.mapping {
		break
	}

	in := make(chan ir.UnifiedStreamEvent, 4)
	in <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "回答引用了 " + ph[:8]}
	in <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: ph[8:]}
	in <- ir.UnifiedStreamEvent{Type: ir.EventDone}
	close(in)

	out := u.wrapStreams(testCtx(), in)
	var collected string
	var sawDone bool
	for ev := range out {
		if ev.Type == ir.EventTextDelta {
			collected += ev.TextDelta
		}
		if ev.Type == ir.EventDone {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("done 事件丢失")
	}
	if !strings.Contains(collected, "sk-abcdefghij0123456789") {
		t.Errorf("跨 chunk 占位符未还原: %q", collected)
	}
	if strings.Contains(collected, placeholderPrefix) {
		t.Errorf("仍有占位符: %q", collected)
	}
}

func TestPrivacyRuleScopes(t *testing.T) {
	// emails 规则关掉时不应遮蔽邮箱。
	e := newTestEngine(t, PrivacyMaskConfig{Enabled: true, Rules: []string{"keys"}})
	req := &ir.UnifiedChatRequest{
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "mail me@example.com"}},
	}
	if u, mErr := e.maskRequest(req); mErr != nil {
		t.Fatalf("maskRequest: %v", mErr)
	} else if u != nil {
		t.Error("关闭 emails 后不应产生遮蔽")
	}
}

func TestPrivacyCustomRule(t *testing.T) {
	e := newTestEngine(t, PrivacyMaskConfig{
		Enabled: true,
		Custom:  []PrivacyCustomRule{{Name: "工号", Pattern: `GH-\d{6}`}},
	})
	req := &ir.UnifiedChatRequest{
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "工号 GH-123456"}},
	}
	u, mErr := e.maskRequest(req)
	if mErr != nil {
		t.Fatalf("maskRequest: %v", mErr)
	}
	if u == nil {
		t.Fatal("自定义规则未生效")
	}
	if strings.Contains(req.Items[0].Text, "GH-123456") {
		t.Errorf("自定义规则未遮蔽: %s", req.Items[0].Text)
	}
}

// 坏正则必须让引擎构建失败：静默跳过一条规则等于把它本该遮蔽的内容明文送出去。
func TestPrivacyBadCustomRuleFailsClosed(t *testing.T) {
	if _, err := newPrivacyEngine(PrivacyMaskConfig{
		Enabled: true,
		Rules:   []string{"keys"},
		Custom:  []PrivacyCustomRule{{Name: "坏的", Pattern: `([unclosed`}},
	}); err == nil {
		t.Fatal("坏正则应返回错误")
	}
}

// 规则名写错导致零条规则编译成功时也必须失败，而不是"开着但什么都不遮"。
func TestPrivacyZeroRulesFailsClosed(t *testing.T) {
	if _, err := newPrivacyEngine(PrivacyMaskConfig{
		Enabled: true,
		Rules:   []string{"no-such-builtin"},
	}); err == nil {
		t.Fatal("零规则应返回错误")
	}
}

// 工具调用参数与工具结果同为自由文本，必须一起遮蔽并在流里还原。
func TestPrivacyMasksToolArgumentsAndOutput(t *testing.T) {
	e := newTestEngine(t, PrivacyMaskConfig{Enabled: true, Rules: []string{"keys", "emails"}})
	req := &ir.UnifiedChatRequest{
		Items: []ir.UnifiedItem{
			{
				Type:      ir.ItemTypeFunctionCall,
				CallID:    "call_1",
				Name:      "http",
				Arguments: `{"Authorization":"Bearer sk-abcdefghij0123456789"}`,
			},
			{
				Type:   ir.ItemTypeFunctionCallOutput,
				CallID: "call_1",
				Output: `{"email":"ops@example.com"}`,
			},
		},
	}
	u, mErr := e.maskRequest(req)
	if mErr != nil {
		t.Fatalf("maskRequest: %v", mErr)
	}
	if u == nil {
		t.Fatal("工具参数/结果应产生遮蔽")
	}
	if strings.Contains(req.Items[0].Arguments, "sk-abcdefghij") {
		t.Errorf("工具参数未遮蔽: %s", req.Items[0].Arguments)
	}
	if strings.Contains(req.Items[1].Output, "ops@example.com") {
		t.Errorf("工具结果未遮蔽: %s", req.Items[1].Output)
	}
	// JSON 结构必须保持可解析（只替换字符串内容，不破坏括号引号）。
	if !strings.HasPrefix(req.Items[0].Arguments, `{"Authorization":"Bearer `) || !strings.HasSuffix(req.Items[0].Arguments, `"}`) {
		t.Errorf("工具参数的 JSON 结构被破坏: %s", req.Items[0].Arguments)
	}

	ph, ok := placeholderFor(u.mapping, "sk-abcdefghij0123456789")
	if !ok {
		t.Fatalf("映射里找不到 key 的占位符: %v", u.mapping)
	}
	got := u.push("调用参数 "+ph[:6]) + u.push(ph[6:]) + u.flush()
	if !strings.Contains(got, "sk-abcdefghij0123456789") {
		t.Errorf("流式还原失败: %q", got)
	}
}

// 占位符由 secret 确定性派生：同一 secret 在两次独立请求里得到同一标签，
// 否则每次请求都换标签，上游前缀缓存会整段失效。
func TestPrivacyPlaceholderDeterministic(t *testing.T) {
	e := newTestEngine(t, PrivacyMaskConfig{Enabled: true, Rules: []string{"keys"}})
	mk := func() string {
		req := &ir.UnifiedChatRequest{
			Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "k=sk-abcdefghij0123456789"}},
		}
		u, mErr := e.maskRequest(req)
		if mErr != nil {
			t.Fatalf("maskRequest: %v", mErr)
		}
		if u == nil {
			t.Fatal("应产生遮蔽")
		}
		for ph := range u.mapping {
			return ph
		}
		return ""
	}
	if a, b := mk(), mk(); a == "" || a != b {
		t.Fatalf("占位符不稳定: %q vs %q", a, b)
	}
	if n := e.placeholderLen; n != len(placeholderPrefix)+placeholderTagLen+len(placeholderSuffix) {
		t.Errorf("placeholderLen 与实际不符: %d", n)
	}
}
