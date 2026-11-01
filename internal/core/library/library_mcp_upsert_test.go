package library

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestSaveMCPUpsertsByName(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "library.json"))
	first, err := s.SaveMCP(MCPEntry{Name: "demo", Config: json.RawMessage(`{"url":"https://a"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !s.UpdateMCPDisabledTools(first.ID, []string{"b"}) {
		t.Fatal("update disabled tools failed")
	}
	second, err := s.SaveMCP(MCPEntry{Name: "demo", Config: json.RawMessage(`{"url":"https://b"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.CreatedAt != first.CreatedAt {
		t.Fatalf("同名保存应复用条目: first=%+v second=%+v", first, second)
	}
	snap := s.List()
	if len(snap.MCPServers) != 1 {
		t.Fatalf("同名保存产生了重复条目: %d", len(snap.MCPServers))
	}
	if got := snap.MCPServers[0].DisabledTools; len(got) != 1 || got[0] != "b" {
		t.Fatalf("工具开关被覆盖: %v", got)
	}
}
