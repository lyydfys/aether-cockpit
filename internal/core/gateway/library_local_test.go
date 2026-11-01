package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/library"
)

func TestInstructionsPathMatchesClientIDs(t *testing.T) {
	home := t.TempDir()
	cases := map[string]string{
		"claude_code": filepath.Join(home, ".claude", "CLAUDE.md"),
		"codex_cli":   filepath.Join(home, ".codex", "AGENTS.md"),
		"gemini_cli":  filepath.Join(home, ".gemini", "GEMINI.md"),
		"opencode":    filepath.Join(home, ".config", "opencode", "AGENTS.md"),
		"mimocode":    filepath.Join(home, ".config", "mimocode", "AGENTS.md"),
	}
	for id, want := range cases {
		if got := instructionsPath(home, id); got != want {
			t.Fatalf("instructionsPath(%q) = %q, want %q", id, got, want)
		}
	}
	if got := instructionsPath("", "claude_code"); got != "" {
		t.Fatalf("空 home 应返回空路径，得到 %q", got)
	}
}

func TestReadLocalText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# 规则\n始终用中文\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	text, size, modified, exists, truncated := readLocalText(path)
	if !exists || truncated || modified == 0 {
		t.Fatalf("exists=%v truncated=%v modified=%d", exists, truncated, modified)
	}
	if size != int64(len("# 规则\n始终用中文\n")) || text != "# 规则\n始终用中文\n" {
		t.Fatalf("size=%d text=%q", size, text)
	}
	if _, _, _, exists, _ := readLocalText(filepath.Join(dir, "missing.md")); exists {
		t.Fatal("不存在的文件不应标记 exists")
	}
}

func TestParseFrontmatter(t *testing.T) {
	raw := "---\nname: reviewer\ndescription: \"审查 diff\"\ntools:\n  - Read\n  - Grep\nmodel: sonnet\n---\n\n你是审查员。\n"
	fields, body := parseFrontmatter(raw)
	if fields["name"] != "reviewer" || fields["description"] != "审查 diff" {
		t.Fatalf("fields = %v", fields)
	}
	if fields["tools"] != "Read, Grep" || fields["model"] != "sonnet" {
		t.Fatalf("tools/model = %q/%q", fields["tools"], fields["model"])
	}
	if body != "你是审查员。\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestRedactFoundServerDetails(t *testing.T) {
	if got := redactServerURL("https://user:pass@example.com/mcp?token=abc#frag"); got != "https://example.com/mcp" {
		t.Fatalf("URL 未脱敏: %q", got)
	}
	if got := redactServerURL("http://127.0.0.1:7993/mcp"); got != "http://127.0.0.1:7993/mcp" {
		t.Fatalf("普通 URL 被改动: %q", got)
	}
	got := redactServerArgs([]string{"--api-key", "sk-1", "--url=https://x", "--token=abc", "serve"})
	want := []string{"--api-key", "***", "--url=https://x", "--token=***", "serve"}
	if len(got) != len(want) {
		t.Fatalf("args = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDiscoverAndImportLocalSubagents(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "reviewer.md"),
		[]byte("---\nname: reviewer\ndescription: 审查 diff\nmodel: sonnet\n---\n\n你是审查员。\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	codexDir := filepath.Join(home, ".codex", "agents")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "helper.toml"),
		[]byte("name = \"helper\"\ndescription = \"执行委派任务\"\nmodel = \"auto\"\ndeveloper_instructions = \"遵守工作区规则。\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	found := discoverSubagents(home, nil, map[string]bool{})
	if len(found) != 2 {
		t.Fatalf("found = %+v", found)
	}
	// 按名字排序：helper 在前。
	if found[0].Name != "helper" || found[0].Agents[0] != "codex_cli" || found[0].Content != "遵守工作区规则。" {
		t.Fatalf("helper = %+v", found[0])
	}
	if found[1].Name != "reviewer" || found[1].Agents[0] != "claude_code" || found[1].Tools != "" {
		t.Fatalf("reviewer = %+v", found[1])
	}

	// 已在库里的名字不再出现。
	known := map[string]bool{"helper": true}
	if got := discoverSubagents(home, nil, known); len(got) != 1 || got[0].Name != "reviewer" {
		t.Fatalf("known 过滤失效: %+v", got)
	}

	store := library.NewStoreWithHome(filepath.Join(t.TempDir(), "library.json"), home)
	g := &Gateway{library: store}
	entry, err := g.importFoundSubagent("helper")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Description != "执行委派任务" || entry.Model != "auto" || entry.Content != "遵守工作区规则。" {
		t.Fatalf("entry = %+v", entry)
	}
	snap := store.List()
	if len(snap.Agents) != 1 || snap.Agents[0].Name != "helper" {
		t.Fatalf("导入后库 = %+v", snap.Agents)
	}
	// 原文件保持不动。
	if _, err := os.Stat(filepath.Join(codexDir, "helper.toml")); err != nil {
		t.Fatalf("导入不应移动原文件: %v", err)
	}
}
