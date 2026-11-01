package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func writeProjectSkill(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverProjectSkills(t *testing.T) {
	dir := t.TempDir()
	writeProjectSkill(t, dir, ".claude/skills/Alpha/SKILL.md", "---\nname: Alpha\ndescription: \"first\"\n---\nbody")
	writeProjectSkill(t, dir, ".agents/skills/Alpha/SKILL.md", "same files")
	writeProjectSkill(t, dir, ".agents/skills/Beta/SKILL.md", "---\ndescription: beta skill\n---\nbeta")
	_ = os.MkdirAll(filepath.Join(dir, ".claude", "skills", "Empty"), 0o755)

	// 已在库里的技能不再列出；同一技能出现在两个目录时 Agent 合并。
	found := discoverProjectSkills([]LibraryProject{{Dir: dir}}, map[string]bool{"Alpha": true})
	if len(found) != 1 || found[0].Name != "Beta" {
		t.Fatalf("found = %+v", found)
	}
	if found[0].Description != "beta skill" {
		t.Fatalf("description = %q", found[0].Description)
	}
	if len(found[0].Agents) != 3 || !hasAgent(found[0].Agents, "codex_cli") ||
		!hasAgent(found[0].Agents, "gemini_cli") || !hasAgent(found[0].Agents, "opencode") {
		t.Fatalf("agents = %v", found[0].Agents)
	}
}

func TestDiscoverProjectSkillsMergesAgents(t *testing.T) {
	dir := t.TempDir()
	writeProjectSkill(t, dir, ".claude/skills/Alpha/SKILL.md", "---\ndescription: first\n---\nbody")
	writeProjectSkill(t, dir, ".agents/skills/Alpha/SKILL.md", "---\ndescription: other\n---\nbody")

	found := discoverProjectSkills([]LibraryProject{{Dir: dir}}, map[string]bool{})
	if len(found) != 1 || found[0].Name != "Alpha" {
		t.Fatalf("found = %+v", found)
	}
	if len(found[0].Agents) != 4 {
		t.Fatalf("agents = %v", found[0].Agents)
	}
	if found[0].Description != "first" {
		t.Fatalf("description = %q", found[0].Description)
	}
}

func TestRemoveFoundSkillDirs(t *testing.T) {
	dir := t.TempDir()
	writeProjectSkill(t, dir, ".claude/skills/Alpha/SKILL.md", "body")
	writeProjectSkill(t, dir, ".agents/skills/Alpha/SKILL.md", "body")
	writeProjectSkill(t, dir, ".claude/skills/Keep/SKILL.md", "body")

	if n := removeFoundSkillDirs([]LibraryProject{{Dir: dir}}, "NotThere"); n != 0 {
		t.Fatalf("不存在时 removed = %d", n)
	}
	if n := removeFoundSkillDirs([]LibraryProject{{Dir: dir}, {Dir: filepath.Join(dir, "missing")}}, "Alpha"); n != 2 {
		t.Fatalf("removed = %d", n)
	}
	for _, rel := range []string{".claude/skills/Alpha", ".agents/skills/Alpha"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("%s 未删除: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", "Keep", "SKILL.md")); err != nil {
		t.Fatalf("Keep 被误删: %v", err)
	}
}

func TestDiscoverProjectServers(t *testing.T) {
	dir := t.TempDir()
	writeProjectSkill(t, dir, ".mcp.json", `{"mcpServers":{"A":{"command":"a"}}}`)
	writeProjectSkill(t, dir, ".gemini/settings.json", `{"mcpServers":{"B":{"httpUrl":"https://b/mcp","headers":{"K":"v"}}}}`)
	writeProjectSkill(t, dir, ".codex/config.toml", "model = \"gpt-5\"\n\n[mcp_servers.C]\ncommand = \"c\"\nargs = [\"-x\"]\n")
	writeProjectSkill(t, dir, "opencode.json", `{"mcp":{"D":{"type":"local","command":["d","--y"],"environment":{"E":"1"}}}}`)

	found := discoverProjectServers([]LibraryProject{{Dir: dir}}, map[string]bool{"B": true})
	if len(found) != 3 {
		t.Fatalf("found = %+v", found)
	}
	byName := map[string]foundProjectServer{}
	for _, f := range found {
		byName[f.Name] = f
	}
	if _, ok := byName["B"]; ok {
		t.Fatal("已进库的 B 不应出现")
	}
	if a := byName["A"]; a.Config["command"] != "a" || !hasAgent(a.Agents, "claude_code") {
		t.Fatalf("A = %+v", a)
	}
	if c := byName["C"]; c.Config["command"] != "c" || !hasAgent(c.Agents, "codex_cli") {
		t.Fatalf("C = %+v", c)
	}
	if d := byName["D"]; d.Config["command"] != "d" || d.Config["env"] == nil || !hasAgent(d.Agents, "opencode") {
		t.Fatalf("D = %+v", d)
	}
}

func TestRemoveFoundServerDirs(t *testing.T) {
	dir := t.TempDir()
	writeProjectSkill(t, dir, ".mcp.json", `{"mcpServers":{"X":{"command":"x"},"Other":{"command":"o"}}}`)
	writeProjectSkill(t, dir, ".gemini/settings.json", `{"mcpServers":{"X":{"httpUrl":"https://x/mcp"}}}`)
	writeProjectSkill(t, dir, ".codex/config.toml", "[mcp_servers.X]\ncommand = \"x\"\n\n[mcp_servers.C]\ncommand = \"c\"\n")
	writeProjectSkill(t, dir, "opencode.json", `{"mcp":{"X":{"type":"local","command":["x"]}}}`)

	if n := removeFoundServerDirs([]LibraryProject{{Dir: dir}}, "Missing"); n != 0 {
		t.Fatalf("不存在时 removed = %d", n)
	}
	if n := removeFoundServerDirs([]LibraryProject{{Dir: dir}}, "X"); n != 1 {
		t.Fatalf("removed = %d", n)
	}
	if found := discoverProjectServers([]LibraryProject{{Dir: dir}}, map[string]bool{}); len(found) != 2 {
		t.Fatalf("X 未删干净: %+v", found)
	}
	for _, name := range []string{"Other", "C"} {
		hit := false
		for _, f := range discoverProjectServers([]LibraryProject{{Dir: dir}}, map[string]bool{}) {
			if f.Name == name {
				hit = true
			}
		}
		if !hit {
			t.Fatalf("删除 X 时误删 %s", name)
		}
	}
}
