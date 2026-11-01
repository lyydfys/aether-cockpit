package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLines(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnalyzeClaudeUsage(t *testing.T) {
	dir := t.TempDir()
	path := writeLines(t, dir, "s.jsonl",
		`{"type":"user","timestamp":"2026-10-28T01:00:00Z","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","timestamp":"2026-10-28T01:00:05Z","uuid":"a1","message":{"id":"m1","model":"claude-sonnet-4-6","content":[{"type":"text"}],"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":50,"cache_creation_input_tokens":10}}}`,
		`{"type":"assistant","timestamp":"2026-10-28T01:00:06Z","uuid":"a1","message":{"id":"m1","model":"claude-sonnet-4-6","content":[{"type":"text"}],"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":50,"cache_creation_input_tokens":10}}}`,
		`{"type":"assistant","timestamp":"2026-10-28T01:05:00Z","uuid":"a2","message":{"id":"m2","model":"claude-haiku-4-5","content":[{"type":"tool_use"}],"usage":{"input_tokens":10,"output_tokens":5}}}`,
	)
	u := Analyze(Session{Agent: "claude-code", Path: path, Last: 1})
	if u.Input != 110 || u.Output != 25 || u.CacheRead != 50 || u.CacheWrite != 10 {
		t.Errorf("usage = %+v", u)
	}
	if u.Messages != 1 || u.ToolCalls != 1 {
		t.Errorf("messages=%d tools=%d", u.Messages, u.ToolCalls)
	}
	if len(u.Models) != 2 || u.Models["claude-sonnet-4-6"].Input != 100 {
		t.Errorf("models = %+v", u.Models)
	}
	// 01:00:00 → 01:00:05 → 01:00:06 → 01:05:00：5 + 1 + 294 = 300 秒
	if u.ActiveSec != 300 {
		t.Errorf("active = %d, want 300", u.ActiveSec)
	}
}

func TestAnalyzeCodexUsage(t *testing.T) {
	dir := t.TempDir()
	path := writeLines(t, dir, "c.jsonl",
		`{"type":"turn_context","timestamp":"2026-10-28T01:59:59Z","payload":{"model":"gpt-6.1-sol"}}`,
		`{"type":"event_msg","timestamp":"2026-10-28T02:00:00Z","payload":{"type":"user_message","message":"hi"}}`,
		`{"type":"event_msg","timestamp":"2026-10-28T02:00:03Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":10,"cached_input_tokens":20}}}}`,
		`{"type":"event_msg","timestamp":"2026-10-28T02:00:04Z","payload":{"type":"agent_message","message":"ok"}}`,
		`{"type":"event_msg","timestamp":"2026-10-28T02:00:09Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":180,"output_tokens":30,"cached_input_tokens":70}}}}`,
		`{"type":"turn_context","timestamp":"2026-10-29T03:00:00Z","payload":{"model":"gpt-6.1-terra"}}`,
		`{"type":"event_msg","timestamp":"2026-10-29T03:00:09Z","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":260,"output_tokens":90,"cached_input_tokens":90,"cache_write_input_tokens":5}}}}`,
	)
	u := Analyze(Session{Agent: "codex", Path: path, Last: 1})
	// input_tokens 包含缓存；归一后 Input=不含缓存的部分，合计=Input+CacheRead+Output。
	if u.Input != 170 || u.Output != 90 || u.CacheRead != 90 || u.CacheWrite != 5 {
		t.Errorf("usage = %+v", u)
	}
	if u.Requests != 3 || u.Messages != 2 || u.ActiveSec != 19 {
		t.Errorf("requests=%d messages=%d active=%d", u.Requests, u.Messages, u.ActiveSec)
	}
	if len(u.Models) != 2 || u.Models["gpt-6.1-sol"].Input != 110 || u.Models["gpt-6.1-terra"].Input != 60 {
		t.Errorf("models = %+v", u.Models)
	}
	if d := u.Days["2026-10-28"]; d == nil || d.Input != 110 || d.CacheRead != 70 {
		t.Errorf("day 10-28 = %+v", d)
	}
	if d := u.Days["2026-10-29"]; d == nil || d.Input != 60 || d.CacheRead != 20 || d.Requests != 1 {
		t.Errorf("day 10-29 = %+v", d)
	}
}
