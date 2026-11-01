package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ApplyMCPTo：把同一条 MCP 服务器写进另一个家目录（WSL 发行版的用法），
// 原 home 不应被写。
func TestApplyMCPToOtherHome(t *testing.T) {
	s, home := newTestStore(t)
	entry, err := s.SaveMCP(MCPEntry{Name: "wsl-demo", Config: json.RawMessage(`{"url":"https://example.com/mcp"}`)})
	if err != nil {
		t.Fatalf("SaveMCP: %v", err)
	}
	other := t.TempDir()
	path, err := s.ApplyMCPTo(other, entry.ID, "claude")
	if err != nil {
		t.Fatalf("ApplyMCPTo: %v", err)
	}
	if !strings.HasPrefix(path, other) {
		t.Fatalf("写到 %q，不在目标家目录 %q", path, other)
	}
	raw, err := os.ReadFile(filepath.Join(other, ".claude.json"))
	if err != nil {
		t.Fatalf("读目标 .claude.json: %v", err)
	}
	if !strings.Contains(string(raw), "wsl-demo") {
		t.Fatalf(".claude.json 未写入服务器: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err == nil {
		t.Fatal("不应写进原 home")
	}
}

// ApplyHookTo / RemoveHookTo：hook 写入并可移除，目标家目录可指定。
func TestApplyHookToOtherHome(t *testing.T) {
	s, home := newTestStore(t)
	h, err := s.SaveHook(HookEntry{Name: "wsl-hook", Event: "before-tool", Command: "echo wsl"})
	if err != nil {
		t.Fatalf("SaveHook: %v", err)
	}
	other := t.TempDir()
	if err := s.ApplyHookTo(other, h, "claude_code"); err != nil {
		t.Fatalf("ApplyHookTo: %v", err)
	}
	settings := filepath.Join(other, ".claude", "settings.json")
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatalf("读目标 settings.json: %v", err)
	}
	if !strings.Contains(string(raw), "echo wsl") || !strings.Contains(string(raw), "PreToolUse") {
		t.Fatalf("hook 未写入目标: %s", raw)
	}
	if err := s.RemoveHookTo(other, h, "claude_code"); err != nil {
		t.Fatalf("RemoveHookTo: %v", err)
	}
	raw, _ = os.ReadFile(settings)
	if strings.Contains(string(raw), "echo wsl") {
		t.Fatalf("hook 未移除: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err == nil {
		t.Fatal("不应写进原 home")
	}
}

// ApplySkillAsTo / RemoveAppliedSkillTo：技能写入指定 home，扫描与移除同源。
func TestApplySkillAsToOtherHome(t *testing.T) {
	s, home := newTestStore(t)
	sk, err := s.SaveSkill(SkillEntry{Name: "wsl-skill", Content: "---\nname: wsl-skill\n---\nbody"})
	if err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	other := t.TempDir()
	path, err := s.ApplySkillAsTo(other, sk.ID, "claude", false)
	if err != nil {
		t.Fatalf("ApplySkillAsTo: %v", err)
	}
	if !strings.HasPrefix(path, other) {
		t.Fatalf("写到 %q，不在目标家目录 %q", path, other)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("技能文件未写入: %v", err)
	}
	if got := s.AppliedSkillAgentsTo(other, "wsl-skill"); len(got) != 1 || got[0] != "claude_code" {
		t.Fatalf("AppliedSkillAgentsTo = %v", got)
	}
	if got := s.AppliedSkillAgents("wsl-skill"); len(got) != 0 {
		t.Fatalf("原 home 不应被写入: %v", got)
	}
	if err := s.RemoveAppliedSkillTo(other, "wsl-skill", "claude"); err != nil {
		t.Fatalf("RemoveAppliedSkillTo: %v", err)
	}
	if got := s.AppliedSkillAgentsTo(other, "wsl-skill"); len(got) != 0 {
		t.Fatalf("移除后仍持有: %v", got)
	}
	_ = home
}

// ApplyAgentTo / RemoveAppliedAgentTo：子代理写入指定 home。
func TestApplyAgentToOtherHome(t *testing.T) {
	s, _ := newTestStore(t)
	a, err := s.SaveAgent(AgentEntry{Name: "wsl-sub", Description: "demo", Content: "you are a helper"})
	if err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	other := t.TempDir()
	path, err := s.ApplyAgentTo(other, a.ID, "claude")
	if err != nil {
		t.Fatalf("ApplyAgentTo: %v", err)
	}
	if !strings.HasPrefix(path, other) {
		t.Fatalf("写到 %q，不在目标家目录 %q", path, other)
	}
	if got := s.AppliedAgentAgentsTo(other, "wsl-sub"); len(got) != 1 || got[0] != "claude_code" {
		t.Fatalf("AppliedAgentAgentsTo = %v", got)
	}
	if err := s.RemoveAppliedAgentTo(other, "wsl-sub", "claude_code"); err != nil {
		t.Fatalf("RemoveAppliedAgentTo: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("移除后文件仍在: %v", err)
	}
}
