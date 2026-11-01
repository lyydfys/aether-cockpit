package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 本机资源发现要覆盖各 Agent 的真实加载目录与“已启用插件”，
// 但不能把商店缓存（Codex .tmp / WorkBuddy marketplace）算进来。
func TestDiscoverLocalSources(t *testing.T) {
	home := t.TempDir()

	// Qoder / WorkBuddy 共用的用户技能。
	writeProjectSkill(t, home, ".qoder/skills/QSkill/SKILL.md", "---\ndescription: qoder skill\n---\nbody")
	writeProjectSkill(t, home, ".workbuddy/skills/QSkill/SKILL.md", "same")
	writeProjectSkill(t, home, ".qoder/settings.json", `{"enabledPlugins":{"Presentations@qoder-marketplace":true,"off@local":false},"mcpServers":{"lighthouse-ops":{"type":"streamableHttp","url":"https://light.example/mcp"}}}`)
	writeProjectSkill(t, home, ".qoder/plugins/cache/qoder-marketplace/Presentations/0.1.3/skills/pptx/SKILL.md", "---\ndescription: slides\n---\nbody")
	writeProjectSkill(t, home, ".qoder/plugins/cache/qoder-marketplace/Presentations/0.1.3/agents/speaker.md", "---\nname: speaker\ndescription: talks\n---\nspeak")
	writeProjectSkill(t, home, ".qoder/plugins/cache/local/off/1/skills/OffSkill/SKILL.md", "body")

	// Claude 已安装插件。
	writeProjectSkill(t, home, ".claude/plugins/installed_plugins.json", `{"plugins":{"demo@official":[{"installPath":"`+filepath.ToSlash(filepath.Join(home, ".claude/plugins/cache/official/demo/1"))+`"}]}}`)
	writeProjectSkill(t, home, ".claude/plugins/cache/official/demo/1/skills/CSkill/SKILL.md", "---\ndescription: claude plugin\n---\nbody")
	writeProjectSkill(t, home, ".claude/plugins/cache/official/demo/1/agents/reviewer.md", "---\nname: reviewer\ndescription: reviews\n---\nreview")

	// Codex 用户技能、内置技能与已启用插件。
	writeProjectSkill(t, home, ".codex/skills/USkill/SKILL.md", "body")
	writeProjectSkill(t, home, ".codex/skills/.system/SSkill/SKILL.md", "body")
	writeProjectSkill(t, home, ".codex/config.toml", "[plugins.\"browser@openai-bundled\"]\nenabled = true\n\n[plugins.\"off@openai-bundled\"]\nenabled = false\n")
	writeProjectSkill(t, home, ".codex/plugins/cache/openai-bundled/browser/1.0/skills/BSkill/SKILL.md", "body")
	writeProjectSkill(t, home, ".codex/plugins/cache/openai-bundled/off/1.0/skills/OffSkill/SKILL.md", "body")

	// Gemini 已安装插件与自带 MCP。
	writeProjectSkill(t, home, ".gemini/config/plugins/science/skills/GSkill/SKILL.md", `---
description: >
  gemini science
  plugin skill
---
body`)
	writeProjectSkill(t, home, ".gemini/config/plugins/science/mcp_config.json", `{"mcpServers":{"science_mcp":{"command":"node","args":["x.js"]}}}`)

	found := discoverAllSkills(nil, home, map[string]bool{})
	byName := map[string]foundProjectSkill{}
	for _, f := range found {
		byName[f.Name] = f
	}
	for _, name := range []string{"QSkill", "pptx", "CSkill", "USkill", "SSkill", "BSkill", "GSkill"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("缺少 %s: %+v", name, byName)
		}
	}
	for _, name := range []string{"OffSkill"} {
		if _, ok := byName[name]; ok {
			t.Fatalf("未启用插件 %s 不应出现", name)
		}
	}
	if q := byName["QSkill"]; len(q.Agents) != 2 || q.Sources == nil || len(q.Sources) < 2 {
		t.Fatalf("QSkill 应合并 Qoder/WorkBuddy: %+v", q)
	}
	if b := byName["BSkill"]; b.Plugin != "browser" || len(b.Agents) != 1 || b.Agents[0] != "codex_cli" {
		t.Fatalf("BSkill = %+v", b)
	}
	if g := byName["GSkill"]; g.Description != "gemini science plugin skill" {
		t.Fatalf("YAML 块 description 未解析: %+v", g)
	}

	agents := discoverSubagents(home, nil, map[string]bool{})
	agentByName := map[string]foundSubagent{}
	for _, a := range agents {
		agentByName[a.Name] = a
	}
	if a, ok := agentByName["reviewer"]; !ok || a.Agents[0] != "claude_code" || a.Plugin != "demo" {
		t.Fatalf("Claude 插件子代理缺失: %+v", agents)
	}
	if a, ok := agentByName["speaker"]; !ok || a.Agents[0] != "qoder" {
		t.Fatalf("Qoder 插件子代理缺失: %+v", agents)
	}

	servers := discoverAllServers(nil, home, map[string]bool{})
	serverByName := map[string]foundProjectServer{}
	for _, s := range servers {
		serverByName[s.Name] = s
	}
	if s, ok := serverByName["lighthouse-ops"]; !ok || s.Agents[0] != "qoder" || s.Config["url"] != "https://light.example/mcp" || s.Config["type"] != "http" {
		t.Fatalf("Qoder MCP 缺失或形状未折算: %+v", serverByName["lighthouse-ops"])
	}
	if s, ok := serverByName["science_mcp"]; !ok || s.Agents[0] != "gemini_cli" {
		t.Fatalf("Gemini 插件 MCP 缺失: %+v", serverByName["science_mcp"])
	}
}

func TestDiscoverExtendedSkillAndAgentRoots(t *testing.T) {
	home := t.TempDir()
	for _, rel := range []string{
		".config/opencode/skills/OpenSkill/SKILL.md",
		".claude-desktop/skills/DesktopSkill/SKILL.md",
		".grok/skills/GrokSkill/SKILL.md",
		".kimi-code/skills/KimiSkill/SKILL.md",
		".minimax/skills/MiMoSkill/SKILL.md",
		".pi/agent/skills/PiSkill/SKILL.md",
	} {
		writeProjectSkill(t, home, rel, "body")
	}
	writeProjectSkill(t, home, ".grok/agents/grok-agent.md", "---\nname: grok-agent\ndescription: g\n---\nbody")
	writeProjectSkill(t, home, ".kimi-code/agents/kimi-agent.md", "---\nname: kimi-agent\ndescription: k\n---\nbody")

	found := discoverAllSkills(nil, home, map[string]bool{})
	byName := map[string]foundProjectSkill{}
	for _, f := range found {
		byName[f.Name] = f
	}
	for _, name := range []string{"OpenSkill", "DesktopSkill", "GrokSkill", "KimiSkill", "MiMoSkill", "PiSkill"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("缺少扩展技能 %s: %+v", name, byName)
		}
	}
	if byName["DesktopSkill"].Agents[0] != "claude_desktop" {
		t.Fatalf("DesktopSkill agent = %v", byName["DesktopSkill"].Agents)
	}

	agents := discoverSubagents(home, nil, map[string]bool{})
	byAgent := map[string]foundSubagent{}
	for _, a := range agents {
		byAgent[a.Name] = a
	}
	for _, name := range []string{"grok-agent", "kimi-agent"} {
		if _, ok := byAgent[name]; !ok {
			t.Fatalf("缺少扩展子代理 %s: %+v", name, byAgent)
		}
	}
}

func TestDiscoverClaudeDesktopMCP(t *testing.T) {
	home := t.TempDir()
	appData := filepath.Join(home, "AppData", "Roaming")
	t.Setenv("APPDATA", appData)
	writeProjectSkill(t, appData, "Claude/claude_desktop_config.json", `{"mcpServers":{"desktop":{"command":"node","args":["m.js"]}}}`)
	writeProjectSkill(t, home, ".gemini/config/mcp_config.json", `{"mcpServers":{"agy":{"command":"agy"}}}`)

	found := discoverAllServers(nil, home, map[string]bool{})
	byName := map[string]foundProjectServer{}
	for _, s := range found {
		byName[s.Name] = s
	}
	if s, ok := byName["desktop"]; !ok || s.Agents[0] != "claude_desktop" || s.Config["command"] != "node" {
		t.Fatalf("Claude Desktop MCP 缺失: %+v", byName["desktop"])
	}
	if s, ok := byName["agy"]; !ok || s.Agents[0] != "gemini_cli" {
		t.Fatalf("Gemini 配置 MCP 缺失: %+v", byName["agy"])
	}
}

func TestDiscoverClaudeProjectsFiltersSystem(t *testing.T) {
	home := t.TempDir()
	valid := filepath.Join(home, "work", "proj-a")
	if err := os.MkdirAll(valid, 0o755); err != nil {
		t.Fatal(err)
	}
	winDir := filepath.Join(home, "fake-windows")
	if err := os.MkdirAll(filepath.Join(winDir, "system32"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WINDIR", winDir)
	t.Setenv("TEMP", filepath.Join(home, "temp"))
	t.Setenv("TMP", filepath.Join(home, "temp"))
	missing := filepath.Join(home, "missing")
	raw, _ := json.Marshal(map[string]any{"projects": map[string]any{
		filepath.ToSlash(valid): map[string]any{}, home: map[string]any{}, filepath.ToSlash(filepath.Join(winDir, "system32")): map[string]any{}, missing: map[string]any{},
	}})
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got := discoverClaudeProjects(home)
	if len(got) != 1 || got[0].Dir != valid || got[0].Name != "proj-a" {
		t.Fatalf("项目发现 = %+v", got)
	}
}

func TestDiscoverCNPluginRoots(t *testing.T) {
	home := t.TempDir()
	// Qoder CN: installed_plugins_v2.json 决定启用与安装路径。
	qoderDir := filepath.Join(home, ".qoder-cn", "plugins", "cache", "qoder-marketplace", "demo", "1.0")
	writeProjectSkill(t, qoderDir, "skills/QSkill/SKILL.md", "---\ndescription: qoder cn\n---\nbody")
	writeProjectSkill(t, qoderDir, "agents/q-agent.md", "---\nname: q-agent\ndescription: qa\n---\nbody")
	writeProjectSkill(t, home, ".qoder-cn/plugins/installed_plugins_v2.json",
		`{"plugins":{"demo@qoder-marketplace":[{"installPath":"`+filepath.ToSlash(qoderDir)+`","enabled":true}],"off@qoder-marketplace":[{"installPath":"`+filepath.ToSlash(filepath.Join(home, "off"))+`","enabled":false}]}}`)

	// WorkBuddy: installed_plugins.json + settings.enabledPlugins 过滤。
	wbOn := filepath.Join(home, ".workbuddy", "plugins", "cache", "workbuddy-builtin", "demo", "1")
	wbOff := filepath.Join(home, ".workbuddy", "plugins", "cache", "workbuddy-builtin", "off", "1")
	writeProjectSkill(t, wbOn, "skills/WBSkill/SKILL.md", "body")
	writeProjectSkill(t, wbOff, "skills/OffWBSkill/SKILL.md", "body")
	writeProjectSkill(t, home, ".workbuddy/settings.json", `{"enabledPlugins":{"demo@workbuddy-builtin":true,"off@workbuddy-builtin":false}}`)
	writeProjectSkill(t, home, ".workbuddy/plugins/installed_plugins.json",
		`{"plugins":{"demo@workbuddy-builtin":[{"installPath":"`+filepath.ToSlash(wbOn)+`"}],"off@workbuddy-builtin":[{"installPath":"`+filepath.ToSlash(wbOff)+`"}]}}`)

	// Trae: 缓存目录多一层版本号。
	writeProjectSkill(t, home, ".trae-cn/plugins/trae-remote-official/tool/1.0/skills/TSkill/SKILL.md", "body")

	found := discoverAllSkills(nil, home, map[string]bool{})
	byName := map[string]foundProjectSkill{}
	for _, s := range found {
		byName[s.Name] = s
	}
	for _, name := range []string{"QSkill", "WBSkill", "TSkill"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("缺少插件技能 %s: %+v", name, byName)
		}
	}
	if _, ok := byName["OffWBSkill"]; ok {
		t.Fatal("禁用插件 OffWBSkill 不应出现")
	}
	if byName["WBSkill"].Agents[0] != "workbuddy" || byName["QSkill"].Agents[0] != "qoder" || byName["TSkill"].Agents[0] != "trae" {
		t.Fatalf("插件技能 agent 映射错误: %+v", byName)
	}

	subs := discoverSubagents(home, nil, map[string]bool{})
	foundAgent := false
	for _, s := range subs {
		if s.Name == "q-agent" {
			foundAgent = true
		}
	}
	if !foundAgent {
		t.Fatalf("Qoder CN 插件子代理缺失: %+v", subs)
	}
}
