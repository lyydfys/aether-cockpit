package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibrarySubagentSaveAdapter(t *testing.T) {
	_, home, h := agentApplyTestGateway(t)
	dir := filepath.Join(home, ".claude", "agents")

	out := applyReq(t, h, "/api/library/subagents/save",
		`{"old":"","subagent":{"name":"helper","description":"Reviews a diff.","prompt":"You are a careful reviewer.","model":"sonnet","tools":"Read, Grep","skills":["sk"],"mcp":["demo"],"agents":["claude_code"]}}`)
	row := findRow(t, out, "subagents", "helper")
	if row["prompt"] != "You are a careful reviewer." {
		t.Fatalf("prompt 未回显: %v", row["prompt"])
	}
	if row["model"] != "sonnet" || row["tools"] != "Read, Grep" {
		t.Fatalf("model/tools 未回显: %v / %v", row["model"], row["tools"])
	}
	if skills, _ := row["skills"].([]any); len(skills) != 1 || skills[0] != "sk" {
		t.Fatalf("skills = %v", row["skills"])
	}
	if got := viewAgents(t, out, "subagents", "helper"); !sameAgents(got, []string{"claude_code"}) {
		t.Fatalf("agents = %v", got)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "helper.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"name: helper", "description: Reviews a diff.", "model: sonnet", "tools: Read, Grep", "You are a careful reviewer."} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("文件缺少 %q: %s", want, raw)
		}
	}

	// 重命名：旧名字的文件要撤掉，新名字写入。
	out = applyReq(t, h, "/api/library/subagents/save",
		`{"old":"helper","subagent":{"name":"helper2","description":"Reviews a diff.","prompt":"You are a careful reviewer.","agents":["claude_code"]}}`)
	findRow(t, out, "subagents", "helper2")
	if _, err := os.Stat(filepath.Join(dir, "helper.md")); !os.IsNotExist(err) {
		t.Fatalf("重命名后旧文件仍在")
	}
	if _, err := os.Stat(filepath.Join(dir, "helper2.md")); err != nil {
		t.Fatalf("新文件未写入: %v", err)
	}
	if rows := namesOf(out, "subagents"); len(rows) != 1 || rows[0] != "helper2" {
		t.Fatalf("库里应只剩新名字: %v", rows)
	}

	// 取消所有 Agent：文件移除，条目仍在库中。
	out = applyReq(t, h, "/api/library/subagents/save",
		`{"old":"helper2","subagent":{"name":"helper2","description":"Reviews a diff.","prompt":"You are a careful reviewer.","agents":[]}}`)
	if _, err := os.Stat(filepath.Join(dir, "helper2.md")); !os.IsNotExist(err) {
		t.Fatalf("取消后文件未删除")
	}
	findRow(t, out, "subagents", "helper2")
}

func findRow(t *testing.T, out map[string]any, list, name string) map[string]any {
	t.Helper()
	rows, _ := out[list].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["name"] == name {
			return row
		}
	}
	t.Fatalf("%s 里没有 %q", list, name)
	return nil
}

func namesOf(out map[string]any, list string) []string {
	rows, _ := out[list].([]any)
	names := make([]string, 0, len(rows))
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if n, ok := row["name"].(string); ok {
			names = append(names, n)
		}
	}
	return names
}
