package pool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/provider"
	_ "aether-cockpit/internal/providers/openaicompat"
)

func TestModelMappingPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	m := New()
	m.SetStorePath(path, nil)
	env := provider.NewAccountEnv("acc-map", "openai-compat")
	env.SetModelMapping(map[string]string{"gpt-4o": "relay-gpt", "claude-*": "relay-claude"})
	m.Add(env)
	if err := m.SaveAccounts(); err != nil {
		t.Fatalf("SaveAccounts: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var f accountsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(f.Accounts) != 1 || f.Accounts[0].ModelMapping["gpt-4o"] != "relay-gpt" {
		t.Fatalf("model_mapping 未落盘: %+v", f.Accounts)
	}

	m2 := New()
	if err := m2.SetStorePath(path, nil); err != nil {
		t.Fatalf("load: %v", err)
	}
	got, ok := m2.Get("acc-map")
	if !ok {
		t.Fatal("账号未恢复")
	}
	if got.MapModel("claude-opus") != "relay-claude" || got.MapModel("gpt-4o") != "relay-gpt" {
		t.Fatalf("映射未恢复: %+v", got.ModelMapping())
	}
}
