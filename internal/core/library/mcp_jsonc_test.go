package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/jsoncutil"
)

func TestApplyMCPOpenCodeJSONC(t *testing.T) {
	s, home := newTestStore(t)
	entry := mustSaveMCP(t, s, "web", `{"type":"http","url":"https://example.com/mcp"}`)
	path := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := "{\n  // keep\n  \"theme\": \"x\",\n  \"mcp\": {\"old\": {\"type\": \"local\", \"command\": [\"a\"]}}\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.ApplyMCP(entry.ID, "opencode")
	if err != nil {
		t.Fatalf("ApplyMCP: %v", err)
	}
	if got != path {
		t.Fatalf("path = %s", got)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "keep") {
		t.Fatalf("注释丢失:\n%s", raw)
	}
	js, err := jsoncutil.Parse(raw)
	if err != nil {
		t.Fatalf("JSONC 无效: %v\n%s", err, raw)
	}
	var doc map[string]any
	if err := json.Unmarshal(js, &doc); err != nil {
		t.Fatal(err)
	}
	mcp := doc["mcp"].(map[string]any)
	if _, ok := mcp["old"]; !ok {
		t.Fatalf("已有服务器丢失: %v", mcp)
	}
	web := mcp["web"].(map[string]any)
	if web["type"] != "remote" || web["url"] != "https://example.com/mcp" {
		t.Fatalf("web = %v", web)
	}
}
