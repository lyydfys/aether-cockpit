package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 三个已支持客户端都要有重开命令（原来只有 Claude Code 有）。
func TestResumeCommandCoversSupportedAgents(t *testing.T) {
	cases := map[string]string{
		"claude-code": "claude --resume abc-123",
		"codex":       "codex resume abc-123",
		"gemini-cli":  "gemini --resume abc-123",
	}
	for agent, want := range cases {
		if got := resumeCommand(agent, "abc-123"); got != want {
			t.Errorf("%s: got %q want %q", agent, got, want)
		}
	}
	// 不支持 / 未知的客户端返回空串（页面据此显示"暂不支持"，而不是给错命令）。
	if got := resumeCommand("kimi-code", "abc"); got != "" {
		t.Errorf("未知客户端应返回空串，得到 %q", got)
	}
	if got := resumeCommand("claude-code", ""); got != "" {
		t.Errorf("空会话 ID 应返回空串，得到 %q", got)
	}
}

// 会话 ID 会被拼进 shell 命令行，必须挡住注入字符。
func TestResumeCommandRejectsUnsafeIDs(t *testing.T) {
	for _, bad := range []string{
		"abc;rm -rf /",
		"abc && curl evil",
		"abc|nc host 1",
		"`whoami`",
		"$(id)",
		"abc\"def",
		strings.Repeat("a", 200),
	} {
		if got := resumeCommand("claude-code", bad); got != "" {
			t.Errorf("危险 ID %q 不该生成命令，得到 %q", bad, got)
		}
	}
	// 合法字符集仍要放行。
	if resumeCommand("codex", "a-b_c.d123") == "" {
		t.Error("安全字符集内的 ID 应放行")
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
