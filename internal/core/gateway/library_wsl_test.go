package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/library"
)

// wslLibraryGateway 造一个带 WSL 覆盖目标（临时目录）与独立库文件的网关。
func wslLibraryGateway(t *testing.T) (*Gateway, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("AETHER_WSL_HOMES", home)
	t.Setenv("AETHER_WSL_DISTRO", "Ubuntu-22.04")
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	// 用临时库文件，避免测试写进仓库 data/library.json。
	g.library = library.NewStore(filepath.Join(t.TempDir(), "library.json"))
	return g, home
}

// 库里的 MCP 服务器可以下发到 WSL 客户端：写进发行版家目录，agentSet 里
// 出现带前缀的 agent，移除后干净。
func TestLibraryServerAppliesToWSLTarget(t *testing.T) {
	g, home := wslLibraryGateway(t)
	if _, err := g.library.SaveMCP(library.MCPEntry{
		Name: "wsl-mcp", Config: json.RawMessage(`{"url":"https://example.com/mcp"}`),
	}); err != nil {
		t.Fatalf("SaveMCP: %v", err)
	}
	agent := "wsl-ubuntu-22-04--claude_code"
	if err := g.addAgentTo("servers", "wsl-mcp", agent, map[string]bool{}); err != nil {
		t.Fatalf("addAgentTo: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("WSL 家目录未写入 .claude.json: %v", err)
	}
	if !strings.Contains(string(raw), "wsl-mcp") {
		t.Fatalf(".claude.json 内容: %s", raw)
	}
	if set := g.agentSet("servers", "wsl-mcp"); !hasAgent(set, agent) {
		t.Fatalf("agentSet(%q) 没报出 WSL agent：%v", "wsl-mcp", set)
	}
	if err := g.removeAgentFrom("servers", "wsl-mcp", agent, map[string]bool{}); err != nil {
		t.Fatalf("removeAgentFrom: %v", err)
	}
	raw, _ = os.ReadFile(filepath.Join(home, ".claude.json"))
	if strings.Contains(string(raw), "wsl-mcp") {
		t.Fatalf("移除后仍在: %s", raw)
	}
}

// 库里的 hook 同样能下发/移除到 WSL 客户端。
func TestLibraryHookAppliesToWSLTarget(t *testing.T) {
	g, home := wslLibraryGateway(t)
	h, err := g.library.SaveHook(library.HookEntry{Name: "wsl-hook", Event: "before-tool", Command: "echo wsl"})
	if err != nil {
		t.Fatalf("SaveHook: %v", err)
	}
	agent := "wsl-ubuntu-22-04--claude_code"
	if err := g.addAgentTo("hooks", "wsl-hook", agent, map[string]bool{}); err != nil {
		t.Fatalf("addAgentTo: %v", err)
	}
	settings := filepath.Join(home, ".claude", "settings.json")
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("读 WSL settings.json: %v", err)
	}
	if !strings.Contains(string(raw), "echo wsl") {
		t.Fatalf("hook 未写进 WSL: %s", raw)
	}
	if err := g.removeAgentFrom("hooks", "wsl-hook", agent, map[string]bool{}); err != nil {
		t.Fatalf("removeAgentFrom: %v", err)
	}
	raw, _ = os.ReadFile(settings)
	if strings.Contains(string(raw), "echo wsl") {
		t.Fatalf("hook 未从 WSL 移除: %s", raw)
	}
	_ = h
}

// 库里的技能也能下发/移除到 WSL 客户端（WSL 走拷贝，避免 9p 上的符号链接）。
func TestLibrarySkillAppliesToWSLTarget(t *testing.T) {
	g, home := wslLibraryGateway(t)
	sk, err := g.library.SaveSkill(library.SkillEntry{Name: "wsl-skill", Content: "---\nname: wsl-skill\n---\nbody"})
	if err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	agent := "wsl-ubuntu-22-04--claude_code"
	if err := g.addAgentTo("skills", "wsl-skill", agent, map[string]bool{}); err != nil {
		t.Fatalf("addAgentTo: %v", err)
	}
	skillFile := filepath.Join(home, ".claude", "skills", "wsl-skill", "SKILL.md")
	raw, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("WSL 技能文件未写入: %v", err)
	}
	if !strings.Contains(string(raw), "wsl-skill") {
		t.Fatalf("技能内容: %s", raw)
	}
	if set := g.agentSet("skills", "wsl-skill"); !hasAgent(set, agent) {
		t.Fatalf("agentSet(skills) 没报出 WSL agent：%v", set)
	}
	if err := g.removeAgentFrom("skills", "wsl-skill", agent, map[string]bool{agent: true}); err != nil {
		t.Fatalf("want 里仍包含该 agent 时不应删除: %v", err)
	}
	if err := g.removeAgentFrom("skills", "wsl-skill", agent, map[string]bool{}); err != nil {
		t.Fatalf("removeAgentFrom: %v", err)
	}
	if _, err := os.Stat(skillFile); !os.IsNotExist(err) {
		t.Fatalf("移除后文件仍在: %v", err)
	}
	_ = sk
}

// 子代理同样支持 WSL（只支持 claude_code 目标）。
func TestLibrarySubagentAppliesToWSLTarget(t *testing.T) {
	g, home := wslLibraryGateway(t)
	if _, err := g.library.SaveAgent(library.AgentEntry{
		Name: "wsl-sub", Description: "demo", Content: "you are a helper",
	}); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	agent := "wsl-ubuntu-22-04--claude_code"
	if err := g.addAgentTo("subagents", "wsl-sub", agent, map[string]bool{}); err != nil {
		t.Fatalf("addAgentTo: %v", err)
	}
	md := filepath.Join(home, ".claude", "agents", "wsl-sub.md")
	raw, err := os.ReadFile(md)
	if err != nil {
		t.Fatalf("WSL 子代理文件未写入: %v", err)
	}
	if !strings.Contains(string(raw), "you are a helper") {
		t.Fatalf("子代理内容: %s", raw)
	}
	if set := g.agentSet("subagents", "wsl-sub"); !hasAgent(set, agent) {
		t.Fatalf("agentSet(subagents) 没报出 WSL agent：%v", set)
	}
	if err := g.removeAgentFrom("subagents", "wsl-sub", agent, map[string]bool{}); err != nil {
		t.Fatalf("removeAgentFrom: %v", err)
	}
	if _, err := os.Stat(md); !os.IsNotExist(err) {
		t.Fatalf("移除后文件仍在: %v", err)
	}
}
