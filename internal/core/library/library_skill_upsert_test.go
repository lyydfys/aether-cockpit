package library

import (
	"path/filepath"
	"testing"
)

func TestSaveSkillUpsertsByName(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "library.json"))
	first, err := s.SaveSkill(SkillEntry{Name: "sk", Content: "v1", Kind: "github", Repo: "o/r", SkillPath: "a/SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.UpdateSkillGroup([]string{"sk"}, "G"); len(got) != 1 {
		t.Fatalf("group = %v", got)
	}
	second, err := s.SaveSkill(SkillEntry{Name: "sk", Content: "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.CreatedAt != first.CreatedAt {
		t.Fatalf("同名保存应复用条目: %+v %+v", first, second)
	}
	snap := s.List()
	if len(snap.Skills) != 1 {
		t.Fatalf("同名保存产生了重复技能: %d", len(snap.Skills))
	}
	got := snap.Skills[0]
	if got.Group != "G" || got.Kind != "github" || got.Repo != "o/r" || got.Content != "v2" {
		t.Fatalf("条目状态未保留: %+v", got)
	}
}
