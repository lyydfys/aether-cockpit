package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 三个已支持客户端都要有重开命令（原来只有 Claude Code 有）。
func TestResumeCommandCoversSupportedAgents(t *testing.T) {
	g := New(Config{UsagePath: "-"})
	cases := map[string]string{
		"claude-code": "claude --resume abc-123",
		"codex":       "codex resume abc-123",
		"gemini-cli":  "gemini --resume abc-123",
	}
	for agent, want := range cases {
		if got := g.resumeCommand(agent, "abc-123"); got != want {
			t.Errorf("%s: got %q want %q", agent, got, want)
		}
	}
	// 不支持 / 未知的客户端返回空串（页面据此显示"暂不支持"，而不是给错命令）。
	if got := g.resumeCommand("kimi-code", "abc"); got != "" {
		t.Errorf("未知客户端应返回空串，得到 %q", got)
	}
	if got := g.resumeCommand("claude-code", ""); got != "" {
		t.Errorf("空会话 ID 应返回空串，得到 %q", got)
	}
}

// 会话 ID 会被拼进 shell 命令行，必须挡住注入字符。
func TestResumeCommandRejectsUnsafeIDs(t *testing.T) {
	g := New(Config{UsagePath: "-"})
	for _, bad := range []string{
		"abc;rm -rf /",
		"abc && curl evil",
		"abc|nc host 1",
		"`whoami`",
		"$(id)",
		"abc\"def",
		strings.Repeat("a", 200),
	} {
		if got := g.resumeCommand("claude-code", bad); got != "" {
			t.Errorf("危险 ID %q 不该生成命令，得到 %q", bad, got)
		}
	}
	// 合法字符集仍要放行。
	if g.resumeCommand("codex", "a-b_c.d123") == "" {
		t.Error("安全字符集内的 ID 应放行")
	}
}

// 用户可以在设置里为任意客户端补命令模板（覆盖内置）。
func TestResumeCommandUserOverride(t *testing.T) {
	g := New(Config{UsagePath: "-"})
	g.settingsMu.Lock()
	g.settings.ResumeCommands = map[string]string{
		"kimi-code":   "kimi resume %s",
		"claude-code": "claude --resume %s --verbose",
	}
	g.settingsMu.Unlock()

	if got := g.resumeCommand("kimi-code", "abc"); got != "kimi resume abc" {
		t.Fatalf("用户模板应生效，得到 %q", got)
	}
	if got := g.resumeCommand("claude-code", "abc"); got != "claude --resume abc --verbose" {
		t.Fatalf("用户模板应覆盖内置，得到 %q", got)
	}
	// 没被覆盖的内置项仍然可用。
	if got := g.resumeCommand("codex", "abc"); got != "codex resume abc" {
		t.Fatalf("未覆盖的内置项应保留，得到 %q", got)
	}
}

// 非法模板（没有 %s / 多个 %s / 带 %% ）不能进终端：退回内置或返回空串。
func TestResumeCommandRejectsBadTemplate(t *testing.T) {
	g := New(Config{UsagePath: "-"})
	g.settingsMu.Lock()
	g.settings.ResumeCommands = map[string]string{
		"claude-code": "claude --resume",        // 缺占位符 → 退回内置
		"kimi-code":   "kimi %s %s",             // 两个占位符 → 未知客户端，无内置 → 空
		"qwen":        "qwen --resume 100%% %s", // 带 %% → 拒绝
	}
	g.settingsMu.Unlock()

	if got := g.resumeCommand("claude-code", "abc"); got != "claude --resume abc" {
		t.Fatalf("非法模板应退回内置，得到 %q", got)
	}
	if got := g.resumeCommand("kimi-code", "abc"); got != "" {
		t.Fatalf("多占位符应被拒，得到 %q", got)
	}
	if got := g.resumeCommand("qwen", "abc"); got != "" {
		t.Fatalf("含 %% 的模板应被拒，得到 %q", got)
	}
}

func TestValidResumeTemplate(t *testing.T) {
	ok := []string{"claude --resume %s", "x %s y"}
	bad := []string{"", "no placeholder", "%s %s", "100%% %s"}
	for _, s := range ok {
		if !validResumeTemplate(s) {
			t.Errorf("%q 应合法", s)
		}
	}
	for _, s := range bad {
		if validResumeTemplate(s) {
			t.Errorf("%q 应非法", s)
		}
	}
}

// 未知客户端走 400，并说明原因。
func TestSessionTerminalRejectsUnknownAgent(t *testing.T) {
	g := New(Config{UsagePath: "-"})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/terminal",
		strings.NewReader(`{"agent":"kimi-code","id":"abc"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.handleSessionTerminal(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("应 400，得到 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "暂不支持") {
		t.Fatalf("应说明暂不支持：%s", rec.Body.String())
	}
}

// 危险会话 ID 同样走 400（不能因为"客户端支持"就放行任意字符串）。
func TestSessionTerminalRejectsUnsafeID(t *testing.T) {
	g := New(Config{UsagePath: "-"})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/terminal",
		strings.NewReader(`{"agent":"claude-code","id":"a;rm -rf /"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	g.handleSessionTerminal(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("应 400，得到 %d: %s", rec.Code, rec.Body.String())
	}
}
