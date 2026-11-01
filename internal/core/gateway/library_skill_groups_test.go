package gateway

import (
	"testing"

	"aether-cockpit/internal/core/library"
)

func TestSkillGroups(t *testing.T) {
	g, _, h := agentApplyTestGateway(t)
	for _, name := range []string{"a", "b"} {
		if _, err := g.library.SaveSkill(library.SkillEntry{
			Name: name, Content: "---\nname: " + name + "\ndescription: d\n---\nbody",
		}); err != nil {
			t.Fatal(err)
		}
	}

	out := applyReq(t, h, "/api/library/skills/group", `{"name":"G","names":["a","b"]}`)
	if got, _ := out["result"].(map[string]any)["changed"].([]any); len(got) != 2 {
		t.Fatalf("changed = %v", got)
	}
	for _, name := range []string{"a", "b"} {
		if row := findRow(t, out, "skills", name); row["mine"] != "G" {
			t.Fatalf("%s mine = %v", name, row["mine"])
		}
	}

	// 整体改名：names 为空时按 old 组集合处理。
	out = applyReq(t, h, "/api/library/skills/group", `{"old":"G","name":"H","names":[]}`)
	for _, name := range []string{"a", "b"} {
		if row := findRow(t, out, "skills", name); row["mine"] != "H" {
			t.Fatalf("改名后 %s mine = %v", name, row["mine"])
		}
	}

	// 只移出一个。
	out = applyReq(t, h, "/api/library/skills/ungroup", `{"name":"H","names":["a"]}`)
	if row := findRow(t, out, "skills", "a"); row["mine"] != "" {
		t.Fatalf("a mine = %v", row["mine"])
	}
	if row := findRow(t, out, "skills", "b"); row["mine"] != "H" {
		t.Fatalf("b mine = %v", row["mine"])
	}

	// 解散整组。
	out = applyReq(t, h, "/api/library/skills/ungroup", `{"name":"H"}`)
	if row := findRow(t, out, "skills", "b"); row["mine"] != "" {
		t.Fatalf("解散后 b mine = %v", row["mine"])
	}
}
