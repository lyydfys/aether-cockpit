package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

func TestProjectSkillPlaceRemove(t *testing.T) {
	dir := t.TempDir()
	if err := placeProjectSkill(dir, "../../evil", "# hi"); err != nil {
		t.Fatal(err)
	}
	// 路径分隔符被清洗，不能逃出项目目录。
	entries, _ := os.ReadDir(filepath.Join(dir, ".claude", "skills"))
	if len(entries) != 1 {
		t.Fatalf("skills = %v", entries)
	}
	got := filepath.Join(dir, ".claude", "skills", entries[0].Name(), "SKILL.md")
	raw, err := os.ReadFile(got)
	if err != nil || string(raw) != "# hi" {
		t.Fatalf("skill file: %v %q", err, raw)
	}
	removeProjectSkill(dir, "../../evil")
	if _, err := os.Stat(got); !os.IsNotExist(err) {
		t.Errorf("skill file not removed")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "skills", entries[0].Name())); !os.IsNotExist(err) {
		t.Errorf("skill dir not removed")
	}
}

func TestProjectServerMergePreservesOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(path, []byte(`{"other":1,"mcpServers":{"keep":{"command":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := placeProjectServer(dir, "live", json.RawMessage(`{"command":"echo","args":["hi"]}`)); err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if root["other"] != float64(1) {
		t.Errorf("other key lost: %v", root)
	}
	servers := root["mcpServers"].(map[string]any)
	if _, ok := servers["keep"]; !ok {
		t.Errorf("existing server lost: %v", servers)
	}
	if _, ok := servers["live"]; !ok {
		t.Errorf("new server missing: %v", servers)
	}
	removeProjectServer(dir, "live")
	raw, _ = os.ReadFile(path)
	root = nil
	_ = json.Unmarshal(raw, &root)
	servers = root["mcpServers"].(map[string]any)
	if _, ok := servers["live"]; ok {
		t.Errorf("live server not removed: %v", servers)
	}
	if _, ok := servers["keep"]; !ok {
		t.Errorf("keep server lost after remove: %v", servers)
	}
}

func TestProjectServerRejectsBrokenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := placeProjectServer(dir, "x", json.RawMessage(`{"command":"y"}`)); err == nil {
		t.Fatal("expected error for broken .mcp.json")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "{not json" {
		t.Errorf("broken file was modified: %q", raw)
	}
}

func TestProjectGitignoreLines(t *testing.T) {
	dir := t.TempDir()
	if err := placeProjectSkill(dir, "demo", "# d"); err != nil {
		t.Fatal(err)
	}
	if err := placeProjectServer(dir, "m", json.RawMessage(`{"command":"x"}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	want := "/.claude/skills/demo/\n/.mcp.json\n"
	if string(raw) != want {
		t.Fatalf("gitignore = %q, want %q", raw, want)
	}
	// 幂等
	if err := placeProjectSkill(dir, "demo", "# d"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(raw) != want {
		t.Fatalf("gitignore dup = %q", raw)
	}
	removeProjectSkill(dir, "demo")
	raw, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(raw) != "/.mcp.json\n" {
		t.Fatalf("gitignore after remove = %q", raw)
	}
}

func TestProjectSkillMultiAgent(t *testing.T) {
	dir := t.TempDir()
	if err := applyProjectSkills(dir, "shared", "# s", []string{"claude_code", "codex_cli"}); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(dir, ".claude", "skills", "shared", "SKILL.md")
	agents := filepath.Join(dir, ".agents", "skills", "shared", "SKILL.md")
	if _, err := os.Stat(claude); err != nil {
		t.Errorf("claude skill missing: %v", err)
	}
	if _, err := os.Stat(agents); err != nil {
		t.Errorf("agents skill missing: %v", err)
	}
	// 只留 Codex：.claude 目标被撤销，.agents 保留
	if err := applyProjectSkills(dir, "shared", "# s", []string{"codex_cli"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(claude); !os.IsNotExist(err) {
		t.Errorf("claude skill should be removed")
	}
	if _, err := os.Stat(agents); err != nil {
		t.Errorf("agents skill lost")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(raw) != "/.agents/skills/shared/\n" {
		t.Errorf("gitignore = %q", raw)
	}
}

func TestProjectMCPMultiAgent(t *testing.T) {
	dir := t.TempDir()
	cfg := json.RawMessage(`{"command":"npx","args":["-y","demo"],"env":{"K":"v"}}`)
	if err := applyProjectMCP(dir, "demo", cfg, []string{"claude_code", "codex_cli", "gemini_cli"}); err != nil {
		t.Fatal(err)
	}
	var claude map[string]any
	raw, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err := json.Unmarshal(raw, &claude); err != nil {
		t.Fatal(err)
	}
	if srv := claude["mcpServers"].(map[string]any)["demo"].(map[string]any); srv["command"] != "npx" {
		t.Fatalf("claude = %v", srv)
	}
	var gem map[string]any
	raw, _ = os.ReadFile(filepath.Join(dir, ".gemini", "settings.json"))
	if err := json.Unmarshal(raw, &gem); err != nil {
		t.Fatal(err)
	}
	gs := gem["mcpServers"].(map[string]any)["demo"].(map[string]any)
	if gs["command"] != "npx" || gs["env"].(map[string]any)["K"] != "v" {
		t.Fatalf("gemini = %v", gs)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	var doc map[string]any
	if err := toml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("codex TOML 不合法: %v\n%s", err, raw)
	}
	cs := doc["mcp_servers"].(map[string]any)["demo"].(map[string]any)
	if cs["command"] != "npx" {
		t.Fatalf("codex = %v", cs)
	}

	if err := applyProjectMCP(dir, "demo", cfg, []string{"gemini_cli"}); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, ".mcp.json"))
	var claude2 map[string]any
	_ = json.Unmarshal(raw, &claude2)
	if len(claude2["mcpServers"].(map[string]any)) != 0 {
		t.Fatalf("claude 条目未删除: %v", claude2)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	if strings.Contains(string(raw), "demo") {
		t.Fatalf("codex 条目未删除: %s", raw)
	}
}

func TestProjectMCPGeminiRemotePreservesSettings(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".gemini"), 0o755)
	path := filepath.Join(dir, ".gemini", "settings.json")
	orig := `{"theme":"dark","mcpServers":{"keep":{"command":"x"}}}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := json.RawMessage(`{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer t"}}`)
	if err := applyProjectMCP(dir, "web", cfg, []string{"gemini_cli"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["theme"] != "dark" {
		t.Fatalf("settings 其他键被改动: %v", doc)
	}
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["keep"]; !ok {
		t.Fatalf("已有服务器丢失: %v", servers)
	}
	web := servers["web"].(map[string]any)
	if web["httpUrl"] != "https://example.com/mcp" {
		t.Fatalf("web = %v", web)
	}
	if _, bad := web["url"]; bad {
		t.Fatalf("streamable HTTP 应写 httpUrl: %v", web)
	}
}

func TestProjectMCPCodexPreservesConfig(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".codex"), 0o755)
	path := filepath.Join(dir, ".codex", "config.toml")
	orig := "model = \"gpt-5\"\n\n[mcp_servers.other]\ncommand = \"other\"\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := json.RawMessage(`{"type":"http","url":"https://example.com/mcp","headers":{"X-K":"v"}}`)
	if err := applyProjectMCP(dir, "web", cfg, []string{"codex_cli"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := toml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("生成的 TOML 不合法: %v\n%s", err, raw)
	}
	if doc["model"] != "gpt-5" {
		t.Fatalf("顶层配置丢失: %v", doc)
	}
	servers := doc["mcp_servers"].(map[string]any)
	if _, ok := servers["other"]; !ok {
		t.Fatalf("其他服务器丢失: %v", servers)
	}
	web := servers["web"].(map[string]any)
	if web["url"] != "https://example.com/mcp" {
		t.Fatalf("web = %v", web)
	}
	if web["http_headers"].(map[string]any)["X-K"] != "v" {
		t.Fatalf("headers = %v", web["http_headers"])
	}

	removeCodexMCP(path, "web")
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "example.com") {
		t.Fatalf("web 段未删除: %s", raw)
	}
	var doc2 map[string]any
	if err := toml.Unmarshal(raw, &doc2); err != nil {
		t.Fatalf("删除后 TOML 不合法: %v\n%s", err, raw)
	}
	if doc2["model"] != "gpt-5" {
		t.Fatalf("删除后顶层配置丢失: %v", doc2)
	}
	if _, ok := doc2["mcp_servers"].(map[string]any)["other"]; !ok {
		t.Fatalf("删除后其他服务器丢失: %v", doc2)
	}
}

func TestProjectMCPCodexRejectsSSEAndBrokenTOML(t *testing.T) {
	dir := t.TempDir()
	err := applyProjectMCP(dir, "sse", json.RawMessage(`{"type":"sse","url":"https://x/sse"}`), []string{"codex_cli"})
	if err == nil || !strings.Contains(err.Error(), "SSE") {
		t.Fatalf("SSE 应被拒绝: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".codex", "config.toml")); !os.IsNotExist(statErr) {
		t.Fatalf("SSE 被拒绝后不应写文件: %v", statErr)
	}

	_ = os.MkdirAll(filepath.Join(dir, ".codex"), 0o755)
	path := filepath.Join(dir, ".codex", "config.toml")
	orig := "model = [broken\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	err = applyProjectMCP(dir, "x", json.RawMessage(`{"command":"npx"}`), []string{"codex_cli"})
	if err == nil || !strings.Contains(err.Error(), "不是合法 TOML") {
		t.Fatalf("坏文件应被拒绝: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != orig {
		t.Fatalf("坏文件被改动: %s", raw)
	}
}

func TestProjectMCPOpenCode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	orig := `{"$schema":"https://opencode.ai/config.json","theme":"x","mcp":{"keep":{"type":"local","command":["a"]}}}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := json.RawMessage(`{"type":"http","url":"https://example.com/mcp","headers":{"K":"v"}}`)
	if err := applyProjectMCP(dir, "web", cfg, []string{"opencode"}); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	doc := read()
	if doc["$schema"] != "https://opencode.ai/config.json" || doc["theme"] != "x" {
		t.Fatalf("顶层键被改动: %v", doc)
	}
	mcp := doc["mcp"].(map[string]any)
	if _, ok := mcp["keep"]; !ok {
		t.Fatalf("已有服务器丢失: %v", mcp)
	}
	web := mcp["web"].(map[string]any)
	if web["type"] != "remote" || web["url"] != "https://example.com/mcp" || web["enabled"] != true {
		t.Fatalf("remote 形状不对: %v", web)
	}
	if web["headers"].(map[string]any)["K"] != "v" {
		t.Fatalf("headers = %v", web["headers"])
	}

	stdio := json.RawMessage(`{"command":"npx","args":["-y","demo"],"env":{"E":"1"}}`)
	if err := applyProjectMCP(dir, "web", stdio, []string{"opencode"}); err != nil {
		t.Fatal(err)
	}
	web = read()["mcp"].(map[string]any)["web"].(map[string]any)
	if web["type"] != "local" || web["enabled"] != true {
		t.Fatalf("local 形状不对: %v", web)
	}
	cmd := web["command"].([]any)
	if len(cmd) != 3 || cmd[0] != "npx" || cmd[2] != "demo" {
		t.Fatalf("command = %v", cmd)
	}
	if web["environment"].(map[string]any)["E"] != "1" {
		t.Fatalf("environment = %v", web["environment"])
	}
	if _, bad := web["url"]; bad {
		t.Fatalf("local 不应带 url: %v", web)
	}

	if err := applyProjectMCP(dir, "web", nil, nil); err != nil {
		t.Fatal(err)
	}
	mcp = read()["mcp"].(map[string]any)
	if _, ok := mcp["web"]; ok {
		t.Fatalf("web 未删除: %v", mcp)
	}
	if _, ok := mcp["keep"]; !ok {
		t.Fatalf("删除时误删已有服务器: %v", mcp)
	}
}

func TestProjectMCPOpenCodeNativeServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	orig := `{"mcp":{"servers":{"keep":{"type":"local","command":["a"]}}}}`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := json.RawMessage(`{"type":"http","url":"https://example.com/mcp"}`)
	if err := applyProjectMCP(dir, "web", cfg, []string{"opencode"}); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	mcp := doc["mcp"].(map[string]any)
	if _, v1 := mcp["web"]; v1 {
		t.Fatalf("原生布局下不应写 mcp.web: %v", mcp)
	}
	servers := mcp["servers"].(map[string]any)
	if _, ok := servers["keep"]; !ok {
		t.Fatalf("已有原生服务器丢失: %v", servers)
	}
	web := servers["web"].(map[string]any)
	if web["type"] != "remote" || web["url"] != "https://example.com/mcp" {
		t.Fatalf("原生服务器形状不对: %v", web)
	}

	if err := applyProjectMCP(dir, "web", nil, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	var doc2 map[string]any
	_ = json.Unmarshal(raw, &doc2)
	servers = doc2["mcp"].(map[string]any)["servers"].(map[string]any)
	if _, ok := servers["web"]; ok {
		t.Fatalf("web 未删除: %v", servers)
	}
	if _, ok := servers["keep"]; !ok {
		t.Fatalf("删除时误删: %v", servers)
	}
}
