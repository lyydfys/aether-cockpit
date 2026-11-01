package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/library"
)

func TestHomeServerAgentsCache(t *testing.T) {
	home := t.TempDir()
	store := library.NewStoreWithHome(filepath.Join(t.TempDir(), "library.json"), home)
	g := &Gateway{library: store}
	path := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"A":{"command":"a"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := g.homeServerAgents()
	if len(m["A"]) != 1 || m["A"][0] != "claude_code" {
		t.Fatalf("A = %v", m)
	}
	if _, ok := m["B"]; ok {
		t.Fatal("B 不应存在")
	}

	if err := os.WriteFile(path, []byte(`{"mcpServers":{"A":{"command":"a"},"B":{"command":"b"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	m = g.homeServerAgents()
	if len(m["B"]) != 1 || m["B"][0] != "claude_code" {
		t.Fatalf("缓存未失效，B = %v", m)
	}
}
