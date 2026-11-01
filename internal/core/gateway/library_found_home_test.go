package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverAllSkillsIncludesAgentHome(t *testing.T) {
	home := t.TempDir()
	writeProjectSkill(t, home, ".claude/skills/HomeSkill/SKILL.md", "---\ndescription: home skill\n---\nbody")
	writeProjectSkill(t, home, ".agents/skills/HomeSkill/SKILL.md", "same")
	writeProjectSkill(t, home, ".agents/skills/OnlyAgents/SKILL.md", "body")
	writeProjectSkill(t, home, ".agents/skills/LibSkill/SKILL.md", "body")

	proj := t.TempDir()
	writeProjectSkill(t, proj, ".claude/skills/ProjSkill/SKILL.md", "body")

	found := discoverAllSkills([]LibraryProject{{Dir: proj}}, home, map[string]bool{"LibSkill": true})
	byName := map[string]foundProjectSkill{}
	for _, f := range found {
		byName[f.Name] = f
	}
	if _, ok := byName["LibSkill"]; ok {
		t.Fatal("库内技能不应出现")
	}
	homeSkill, ok := byName["HomeSkill"]
	if !ok || len(homeSkill.Agents) != 4 {
		t.Fatalf("HomeSkill = %+v", homeSkill)
	}
	if homeSkill.Description != "home skill" {
		t.Fatalf("description = %q", homeSkill.Description)
	}
	if only, ok := byName["OnlyAgents"]; !ok || len(only.Agents) != 3 {
		t.Fatalf("OnlyAgents = %+v", only)
	}
	if _, ok := byName["ProjSkill"]; !ok {
		t.Fatal("项目技能丢失")
	}
}

func TestDiscoverAllSkillsMergesProjectAndHome(t *testing.T) {
	home := t.TempDir()
	writeProjectSkill(t, home, ".claude/skills/Same/SKILL.md", "home")
	proj := t.TempDir()
	writeProjectSkill(t, proj, ".agents/skills/Same/SKILL.md", "project")

	found := discoverAllSkills([]LibraryProject{{Dir: proj}}, home, map[string]bool{})
	if len(found) != 1 {
		t.Fatalf("found = %+v", found)
	}
	if len(found[0].Agents) != 4 {
		t.Fatalf("agents = %v", found[0].Agents)
	}
}

func TestRemoveSkillDirFromHome(t *testing.T) {
	home := t.TempDir()
	writeProjectSkill(t, home, ".claude/skills/Gone/SKILL.md", "body")
	writeProjectSkill(t, home, ".agents/skills/Gone/SKILL.md", "body")
	writeProjectSkill(t, home, ".agents/skills/Keep/SKILL.md", "body")

	if !removeSkillDir(filepath.Join(home, ".claude", "skills"), "Gone") {
		t.Fatal("应删除 .claude/skills/Gone")
	}
	if !removeSkillDir(filepath.Join(home, ".agents", "skills"), "Gone") {
		t.Fatal("应删除 .agents/skills/Gone")
	}
	if removeSkillDir(filepath.Join(home, ".agents", "skills"), "Missing") {
		t.Fatal("不存在时不应删除")
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "Keep", "SKILL.md")); err != nil {
		t.Fatalf("Keep 被误删: %v", err)
	}
	// 没有 SKILL.md 的同名目录不动。
	empty := filepath.Join(home, ".agents", "skills", "Empty")
	_ = os.MkdirAll(empty, 0o755)
	if removeSkillDir(filepath.Join(home, ".agents", "skills"), "Empty") {
		t.Fatal("无 SKILL.md 的目录不应删除")
	}
}
