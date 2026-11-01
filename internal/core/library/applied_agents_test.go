package library

import "testing"

func TestAppliedSkillAgents(t *testing.T) {
	s, _ := newTestStore(t)
	e, err := s.SaveSkill(SkillEntry{Name: "A", Content: "# A"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.AppliedSkillAgents("A"); len(got) != 0 {
		t.Fatalf("未下发时 = %v", got)
	}
	if _, err := s.ApplySkillAs(e.ID, "claude", false); err != nil {
		t.Fatal(err)
	}
	if got := s.AppliedSkillAgents("A"); len(got) != 1 || got[0] != "claude_code" {
		t.Fatalf("claude = %v", got)
	}
	if _, err := s.ApplySkillAs(e.ID, "agents", false); err != nil {
		t.Fatal(err)
	}
	if got := s.AppliedSkillAgents("A"); len(got) != 4 {
		t.Fatalf("全部 = %v", got)
	}
}
