package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConcurrentApplyMCP(t *testing.T) {
	s, home := newTestStore(t)
	ids := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		e, err := s.SaveMCP(MCPEntry{Name: string(rune('a' + i)), Config: json.RawMessage(`{"command":"npx"}`)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}
	done := make(chan error, len(ids))
	for _, id := range ids {
		go func(id string) {
			_, err := s.ApplyMCP(id, "claude")
			done <- err
		}(id)
	}
	for range ids {
		if err := <-done; err != nil {
			t.Fatalf("ApplyMCP: %v", err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if len(servers) != 8 {
		t.Fatalf("并发写入丢了条目: %d", len(servers))
	}
}

func TestConcurrentApplySkill(t *testing.T) {
	s, home := newTestStore(t)
	ids := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		e, err := s.SaveSkill(SkillEntry{Name: "s" + string(rune('a'+i)), Content: "# x"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
	}
	done := make(chan error, len(ids))
	for _, id := range ids {
		go func(id string) {
			_, err := s.ApplySkillAs(id, "claude", false)
			done <- err
		}(id)
	}
	for range ids {
		if err := <-done; err != nil {
			t.Fatalf("ApplySkillAs: %v", err)
		}
	}
	for i := 0; i < 8; i++ {
		p := filepath.Join(home, ".claude", "skills", "s"+string(rune('a'+i)), "SKILL.md")
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("缺失 %s: %v", p, err)
		}
	}
}
