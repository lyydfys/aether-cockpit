package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/jsoncutil"
)

func TestProjectMCPOpenCodeJSONC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.jsonc")
	orig := "{\n  // keep this comment\n  \"theme\": \"x\",\n  \"mcp\": {\"old\": {\"type\": \"local\", \"command\": [\"a\"]}}\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := json.RawMessage(`{"type":"http","url":"https://example.com/mcp","headers":{"K":"v"}}`)
	if err := applyProjectMCP(dir, "web", cfg, []string{"opencode"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "keep this comment") {
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
	if doc["theme"] != "x" {
		t.Fatalf("theme = %v", doc["theme"])
	}

	if err := applyProjectMCP(dir, "web", nil, nil); err != nil {
		t.Fatalf("remove: %v", err)
	}
	raw, _ = os.ReadFile(path)
	if !strings.Contains(string(raw), "keep this comment") {
		t.Fatalf("删除后注释丢失:\n%s", raw)
	}
	js, _ = jsoncutil.Parse(raw)
	if err := json.Unmarshal(js, &doc); err != nil {
		t.Fatal(err)
	}
	mcp = doc["mcp"].(map[string]any)
	if _, ok := mcp["web"]; ok {
		t.Fatalf("web 未删除: %v", mcp)
	}
	if _, ok := mcp["old"]; !ok {
		t.Fatalf("删除时误删 old: %v", mcp)
	}
}

func TestDiscoverOpenCodeJSONCServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.jsonc")
	orig := "{\n  // found\n  \"mcp\": {\"jsoncSrv\": {\"type\": \"local\", \"command\": [\"j\"]}}\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	found := discoverProjectServers([]LibraryProject{{Dir: dir}}, map[string]bool{})
	if len(found) != 1 || found[0].Name != "jsoncSrv" {
		t.Fatalf("found = %+v", found)
	}
	if found[0].Config["command"] != "j" {
		t.Fatalf("config = %v", found[0].Config)
	}
}
