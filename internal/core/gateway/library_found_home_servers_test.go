package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func writeHomeFile(t *testing.T, home, rel, content string) {
	t.Helper()
	path := filepath.Join(home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverAllServersIncludesAgentHome(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".claude.json", `{"mcpServers":{"H1":{"command":"h1"},"Lib":{"command":"l"}}}`)
	writeHomeFile(t, home, ".gemini/settings.json", `{"mcpServers":{"H2":{"httpUrl":"https://h2/mcp"}}}`)
	writeHomeFile(t, home, ".codex/config.toml", "[mcp_servers.H3]\ncommand = \"h3\"\n")
	writeHomeFile(t, home, ".config/opencode/opencode.json", `{"mcp":{"H4":{"type":"local","command":["h4"]}}}`)

	proj := t.TempDir()
	writeHomeFile(t, proj, ".mcp.json", `{"mcpServers":{"H1":{"command":"h1"},"P1":{"command":"p"}}}`)

	found := discoverAllServers([]LibraryProject{{Dir: proj}}, home, map[string]bool{"Lib": true})
	byName := map[string]foundProjectServer{}
	for _, f := range found {
		byName[f.Name] = f
	}
	if _, ok := byName["Lib"]; ok {
		t.Fatal("库内服务器不应出现")
	}
	for _, name := range []string{"H1", "H2", "H3", "H4", "P1"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("%s 丢失: %+v", name, found)
		}
	}
	if len(byName["H1"].Agents) != 1 {
		t.Fatalf("H1 项目与全局同名应合并为 1 个 Agent: %v", byName["H1"].Agents)
	}
	if byName["H2"].Config["url"] != "https://h2/mcp" || mcpTransport(byName["H2"].Config) != "http" {
		t.Fatalf("H2 = %+v", byName["H2"])
	}
	if byName["H3"].Config["command"] != "h3" {
		t.Fatalf("H3 = %+v", byName["H3"])
	}
	if byName["H4"].Config["command"] != "h4" {
		t.Fatalf("H4 = %+v", byName["H4"])
	}
}

func TestRemoveHomeServerAcrossFormats(t *testing.T) {
	home := t.TempDir()
	writeHomeFile(t, home, ".claude.json", `{"theme":"x","mcpServers":{"X":{"command":"x"},"Keep":{"command":"k"}}}`)
	writeHomeFile(t, home, ".gemini/settings.json", `{"mcpServers":{"X":{"httpUrl":"https://x/mcp"},"GKeep":{"command":"g"}}}`)
	writeHomeFile(t, home, ".codex/config.toml", "[mcp_servers.X]\ncommand = \"x\"\n\n[mcp_servers.CKeep]\ncommand = \"c\"\n")
	writeHomeFile(t, home, ".config/opencode/opencode.json", `{"mcp":{"X":{"type":"local","command":["x"]},"OKeep":{"type":"local","command":["o"]}}}`)

	if n := removeHomeServer(home, "Missing"); n != 0 {
		t.Fatalf("不存在时 removed = %d", n)
	}
	if n := removeHomeServer(home, "X"); n != 4 {
		t.Fatalf("removed = %d", n)
	}
	found := discoverHomeServers(home, map[string]bool{})
	byName := map[string]bool{}
	for _, f := range found {
		byName[f.Name] = true
	}
	if byName["X"] {
		t.Fatalf("X 未删干净: %+v", found)
	}
	for _, keep := range []string{"Keep", "GKeep", "CKeep", "OKeep"} {
		if !byName[keep] {
			t.Fatalf("删除 X 时误删 %s: %+v", keep, found)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json.aether.bak")); err != nil {
		t.Fatalf("缺少备份: %v", err)
	}
}
