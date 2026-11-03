package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/redact"
	"aether-cockpit/internal/provider"
)

// 占位符被上游拆成多个 chunk 时，必须跨 chunk 还原，不能把半截占位符发给用户。
func TestUnmaskChunkAcrossSplits(t *testing.T) {
	e := redact.New()
	e.SetEnabled(true)
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	masked := e.Mask("key "+secret+" end", "", "").Text
	i := strings.Index(masked, "{{")
	j := strings.Index(masked, "}}") + 2
	ph := masked[i:j]

	for cut := 1; cut < len(ph); cut++ {
		var pending string
		var got strings.Builder
		got.WriteString(unmaskChunk(e, "", &pending, masked[:i]+ph[:cut], false, false))
		got.WriteString(unmaskChunk(e, "", &pending, ph[cut:]+masked[j:], false, false))
		got.WriteString(unmaskChunk(e, "", &pending, "", true, false))
		if got.String() != "key "+secret+" end" {
			t.Fatalf("cut=%d → %q (pending=%q)", cut, got.String(), pending)
		}
	}
}

func TestUnmaskChunkPlainText(t *testing.T) {
	e := redact.New()
	e.SetEnabled(true)
	var pending string
	if got := unmaskChunk(e, "", &pending, "hello {{not a placeholder", false, false); got != "hello " || !strings.HasPrefix(pending, "{{") {
		t.Fatalf("got=%q pending=%q", got, pending)
	}
	if got := unmaskChunk(e, "", &pending, "}} world", false, false); got != "{{not a placeholder}} world" {
		t.Fatalf("second = %q", got)
	}
	if got := unmaskChunk(e, "", &pending, "", true, false); got != "" {
		t.Fatalf("final = %q", got)
	}
}

/* ---------------- P0-1：block 动作必须拒发 ---------------- */

// 把某个 kind 的处置方式设成 block 后，请求必须在上游调用之前被拒绝，
// 而且返回的错误码要能被下游识别为"策略拒绝"而非"服务不可用"。
func TestRedactBlockRefusesRequest(t *testing.T) {
	eng := redact.New()
	eng.SetEnabled(true)
	eng.SetCustomRules([]redact.CustomRule{
		{Kind: "SECRET", Prefix: "SECRET-DO-NOT-SEND"},
	})
	eng.SetActions(map[string]redact.Action{"SECRET": redact.ActionBlock})

	called := false
	next := StreamHandler(func(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
		called = true
		ch := make(chan ir.UnifiedStreamEvent, 1)
		close(ch)
		return ch, nil
	})

	req := &ir.UnifiedChatRequest{
		ProviderID: "mock",
		Model:      "echo",
		Items: []ir.UnifiedItem{{
			Type: ir.ItemTypeMessage, Role: "user",
			Text: "please carry SECRET-DO-NOT-SEND out",
		}},
	}

	_, err := Redact(Deps{Redact: eng})(next)(context.Background(), nil, req)
	if err == nil {
		t.Fatal("命中 block 规则时应拒绝发送")
	}
	if called {
		t.Error("拒绝发送后不应再调用上游")
	}
	ae := ir.AsAPIError(err)
	if ae.Code != ir.ErrCodePrivacyBlocked {
		t.Errorf("错误码 = %q，期望 %q", ae.Code, ir.ErrCodePrivacyBlocked)
	}
	if got := ae.StatusCode(); got != 403 {
		t.Errorf("状态码 = %d，期望 403（策略拒绝，不是 503 服务不可用）", got)
	}
}

// 即便调用方漏判 Blocked，正文里也绝不能留原文——这是纵深防御的最后一道。
func TestBlockNeverWritesPlaintext(t *testing.T) {
	e := redact.New()
	e.SetEnabled(true)
	e.SetCustomRules([]redact.CustomRule{
		{Kind: "SECRET", Prefix: "SECRET-DO-NOT-SEND"},
	})
	e.SetActions(map[string]redact.Action{"SECRET": redact.ActionBlock})

	secret := "SECRET-DO-NOT-SEND"
	r := e.Mask("please carry "+secret+" out", "", "")
	if !r.Blocked {
		t.Fatal("应被拦截")
	}
	if strings.Contains(r.Text, secret) {
		t.Fatalf("block 分支把原文写回了：%q", r.Text)
	}
	if !strings.Contains(r.Text, "{{SECRET_") {
		t.Fatalf("block 分支应写回占位符，实际 = %q", r.Text)
	}

	// 预览路径必须是同一语义：页面不能展示一段"会被拦截"的明文。
	p, _ := e.Preview("please carry " + secret + " out")
	if !p.Blocked {
		t.Fatal("Preview 应标记 blocked")
	}
	if strings.Contains(p.Text, secret) {
		t.Fatalf("Preview 的 block 分支把原文写回了：%q", p.Text)
	}
}

/* ---------------- P0-2：工具参数/结果/附件名必须脱敏 ---------------- */

// Agent 场景里最高频的泄露是工具参数（{"Authorization":"Bearer …"}）与
// 命令回显。默认路径曾经完全不碰它们。
func TestRedactCoversToolArgumentsOutputAndFileName(t *testing.T) {
	eng := redact.New()
	eng.SetEnabled(true)

	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	email := "alice@example.com"

	next := StreamHandler(func(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
		// 这里是"上游看到的请求"，三种字段都不能再有原文。
		it := req.Items[0]
		if it.Text != "" {
			if strings.Contains(it.Text, email) {
				t.Errorf("Text 仍含明文邮箱：%q", it.Text)
			}
		}
		if strings.Contains(it.Arguments, secret) {
			t.Errorf("Arguments 仍含明文密钥：%q", it.Arguments)
		}
		if !json.Valid([]byte(it.Arguments)) {
			t.Errorf("Arguments 脱敏后不是合法 JSON：%q", it.Arguments)
		}
		if strings.Contains(it.Output, secret) {
			t.Errorf("Output 仍含明文密钥：%q", it.Output)
		}
		if strings.Contains(it.Attachments[0].FileName, secret) {
			t.Errorf("附件名仍含明文密钥：%q", it.Attachments[0].FileName)
		}
		ch := make(chan ir.UnifiedStreamEvent, 1)
		close(ch)
		return ch, nil
	})

	req := &ir.UnifiedChatRequest{
		ProviderID: "mock",
		Model:      "echo",
		Items: []ir.UnifiedItem{
			{
				Type: ir.ItemTypeMessage, Role: "user",
				Text:      "my email is " + email,
				Arguments: `{"Authorization":"Bearer ` + secret + `","path":"main.go","n":3}`,
				Output:    "export KEY=" + secret,
				Attachments: []ir.MediaAttachment{
					{Kind: "file", FileName: "dump-" + secret + ".txt"},
				},
			},
		},
	}

	ch, err := Redact(Deps{Redact: eng})(next)(context.Background(), nil, req)
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	for range ch {
	}
}

// Arguments 是 JSON 字符串：脱敏必须逐层走字符串叶子，不能用占位符把
// 键名/数字结构弄坏。
func TestMaskArgumentsKeepsJSONShape(t *testing.T) {
	eng := redact.New()
	eng.SetEnabled(true)

	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	var blockedBy string
	var hits []redact.Hit
	out := maskArguments(eng,
		`{"cmd":"curl -H 'Authorization: `+secret+`'","n":42,"deep":{"list":["`+secret+`"]}}`,
		"mock", "echo", "k1", "agent", "function_call", &blockedBy, &hits)

	if !json.Valid([]byte(out)) {
		t.Fatalf("脱敏后不是合法 JSON：%q", out)
	}
	if strings.Contains(out, secret) {
		t.Fatalf("仍含明文密钥：%q", out)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if v["n"].(float64) != 42 {
		t.Errorf("数字被改动：%v", v["n"])
	}
	if _, ok := v["cmd"]; !ok {
		t.Error("键名丢失")
	}
}

// 解析不了的参数（上游偶发返回半截 JSON）退回整串脱敏，宁可换编码也不漏。
func TestMaskArgumentsFallsBackOnInvalidJSON(t *testing.T) {
	eng := redact.New()
	eng.SetEnabled(true)
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	var blockedBy string
	var hits []redact.Hit
	out := maskArguments(eng, `{"cmd": "`+secret, "mock", "echo", "k", "a", "function_call", &blockedBy, &hits)
	if strings.Contains(out, secret) {
		t.Fatalf("非法 JSON 时漏脱敏：%q", out)
	}
}
