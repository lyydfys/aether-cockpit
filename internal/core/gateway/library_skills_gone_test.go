package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/library"
)

func TestRemoveGoneSkills(t *testing.T) {
	g, home, h := agentApplyTestGateway(t)
	gone, err := g.library.SaveSkill(library.SkillEntry{
		Name: "gone", Content: "---\nname: gone\ndescription: d\n---\nbody",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.library.SaveSkill(library.SkillEntry{
		Name: "stay", Content: "---\nname: stay\ndescription: d\n---\nbody",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.library.ApplySkillAs(gone.ID, "claude", false); err != nil {
		t.Fatal(err)
	}
	g.setSkillCheck("gone", "gone", "来源 404")

	out := applyReq(t, h, "/api/library/skills/remove-gone", `{"names":["gone","stay","missing"]}`)
	res, _ := out["result"].(map[string]any)
	changed, _ := res["changed"].([]any)
	if len(changed) != 1 || changed[0] != "gone" {
		t.Fatalf("changed = %v", changed)
	}
	un, _ := res["unremoved"].([]any)
	if len(un) != 2 {
		t.Fatalf("unremoved = %v", un)
	}
	if rows := namesOf(out, "skills"); len(rows) != 1 || rows[0] != "stay" {
		t.Fatalf("库中技能 = %v", rows)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "gone")); !os.IsNotExist(err) {
		t.Fatalf("已下发的技能目录未移除")
	}
	if dir := g.library.BackupDir(); dir == "" {
		t.Fatalf("备份目录不可用")
	} else if entries, _ := os.ReadDir(dir); len(entries) == 0 {
		t.Fatalf("内容没有进入集中备份")
	}
}
