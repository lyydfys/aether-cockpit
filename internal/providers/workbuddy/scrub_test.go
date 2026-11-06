package workbuddy

import (
	"strings"
	"testing"
)

func payloadWithSystem(system string) map[string]any {
	return map[string]any{
		"model": "glm-5",
		"messages": []map[string]any{
			{"role": "system", "content": system},
			{"role": "user", "content": "帮我看看这个函数"},
		},
	}
}

// Claude Code 的开场白指纹必须被替换掉。
func TestScrubClaudeFingerprint(t *testing.T) {
	p := payloadWithSystem("You are Claude Code, Anthropic's official CLI for Claude.\n\n<env>cwd=/tmp</env>")
	if !scrubClaudeFingerprint(p) {
		t.Fatal("应确实改写过")
	}
	msgs := p["messages"].([]map[string]any)
	got := msgs[0]["content"].(string)
	if strings.Contains(got, "Claude Code") || strings.Contains(got, "Anthropic") {
		t.Fatalf("指纹未清洗：%q", got)
	}
	if !strings.Contains(got, "helpful AI coding assistant") {
		t.Fatalf("应替换成中性描述：%q", got)
	}
	if !strings.Contains(got, "cwd=/tmp") {
		t.Fatalf("环境块不应被破坏：%q", got)
	}
	if msgs[1]["content"] != "帮我看看这个函数" {
		t.Fatal("用户消息不应被改动")
	}
}

// 没有指纹的 system 保持原样，返回 false。
func TestScrubNoFingerprintUntouched(t *testing.T) {
	p := payloadWithSystem("You are a helpful assistant.")
	if scrubClaudeFingerprint(p) {
		t.Fatal("没有指纹时不应报告改写过")
	}
	if got := p["messages"].([]map[string]any)[0]["content"]; got != "You are a helpful assistant." {
		t.Fatalf("内容被改了：%q", got)
	}
}

// 只处理第一条 system：用户正文里的同名字样不能动。
func TestScrubOnlySystemMessage(t *testing.T) {
	p := map[string]any{"messages": []map[string]any{
		{"role": "user", "content": "You are Claude Code, Anthropic's official CLI for Claude."},
	}}
	if scrubClaudeFingerprint(p) {
		t.Fatal("用户消息不应被清洗")
	}
	if p["messages"].([]map[string]any)[0]["content"] == nil {
		t.Fatal("内容丢失")
	}
}

// 开关解析：默认关闭，只有显式真值才开。
func TestScrubEnabledFlag(t *testing.T) {
	creds := map[string]string{}
	get := func(k string) string { return creds[k] }
	if scrubEnabled(get) {
		t.Fatal("默认必须关闭")
	}
	for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
		creds["sanitize_claude_prompt"] = v
		if !scrubEnabled(get) {
			t.Fatalf("%q 应视为开启", v)
		}
	}
	for _, v := range []string{"0", "false", "", "no"} {
		creds["sanitize_claude_prompt"] = v
		if scrubEnabled(get) {
			t.Fatalf("%q 应视为关闭", v)
		}
	}
}
