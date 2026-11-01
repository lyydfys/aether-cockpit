package library

import (
	"os"
	"path/filepath"
	"testing"
)

func hookCommand(t *testing.T, root map[string]any, event string) (map[string]any, bool) {
	t.Helper()
	hooks, _ := root["hooks"].(map[string]any)
	arr, _ := hooks[event].([]any)
	for _, e := range arr {
		m, _ := e.(map[string]any)
		cmds, _ := m["hooks"].([]any)
		for _, c := range cmds {
			cm, _ := c.(map[string]any)
			if cmd, _ := cm["command"].(string); cmd == "echo hi" {
				return m, true
			}
		}
	}
	return nil, false
}

func TestApplyHookFormats(t *testing.T) {
	s, home := newTestStore(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(settings), 0o755)
	_ = os.WriteFile(settings, []byte(`{"theme":"x","hooks":{"Stop":[{"hooks":[{"type":"command","command":"user"}]}]}}`), 0o644)

	h := HookEntry{Name: "notify", Event: "turn-end", Matcher: "shell", Command: "echo hi", Timeout: 5}
	for _, a := range []string{"claude_code", "codex_cli", "gemini_cli"} {
		if err := s.ApplyHook(h, a); err != nil {
			t.Fatalf("ApplyHook(%s): %v", a, err)
		}
	}
	// 幂等：重复写不会重复条目。
	if err := s.ApplyHook(h, "claude_code"); err != nil {
		t.Fatal(err)
	}

	root, err := readHookRoot(settings)
	if err != nil {
		t.Fatal(err)
	}
	if root["theme"] != "x" {
		t.Fatalf("其他键丢失: %v", root)
	}
	hooks := root["hooks"].(map[string]any)
	arr := hooks["Stop"].([]any)
	if len(arr) != 2 {
		t.Fatalf("Stop 条数 = %d（用户 1 + 我们 1）", len(arr))
	}
	e, ok := hookCommand(t, root, "Stop")
	if !ok || e["matcher"] != "Bash" {
		t.Fatalf("claude 条目 = %v", e)
	}
	cmd := e["hooks"].([]any)[0].(map[string]any)
	if cmd["timeout"] != float64(5) {
		t.Fatalf("claude timeout = %v", cmd["timeout"])
	}

	codex, _ := readHookRoot(filepath.Join(home, ".codex", "hooks.json"))
	e, ok = hookCommand(t, codex, "Stop")
	if !ok || e["matcher"] != "Bash" {
		t.Fatalf("codex 条目 = %v", e)
	}
	cmd = e["hooks"].([]any)[0].(map[string]any)
	if cmd["timeout"] != float64(5) {
		t.Fatalf("codex timeout = %v", cmd["timeout"])
	}

	gem, _ := readHookRoot(filepath.Join(home, ".gemini", "settings.json"))
	e, ok = hookCommand(t, gem, "AfterAgent")
	if !ok || e["matcher"] != "run_shell_command" {
		t.Fatalf("gemini 条目 = %v", e)
	}
	cmd = e["hooks"].([]any)[0].(map[string]any)
	if cmd["timeout"] != float64(5000) {
		t.Fatalf("gemini timeout（毫秒）= %v", cmd["timeout"])
	}
}

func TestRemoveHookKeepsUser(t *testing.T) {
	s, home := newTestStore(t)
	settings := filepath.Join(home, ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(settings), 0o755)
	_ = os.WriteFile(settings, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"user"}]}]}}`), 0o644)
	h := HookEntry{Name: "notify", Event: "turn-end", Command: "echo hi", Timeout: 5}
	if err := s.ApplyHook(h, "claude_code"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveHook(h, "claude_code"); err != nil {
		t.Fatal(err)
	}
	root, _ := readHookRoot(settings)
	arr := root["hooks"].(map[string]any)["Stop"].([]any)
	if len(arr) != 1 {
		t.Fatalf("移除后条数 = %d", len(arr))
	}
	cmds := arr[0].(map[string]any)["hooks"].([]any)
	if cmds[0].(map[string]any)["command"] != "user" {
		t.Fatalf("用户条目被删: %v", arr)
	}
}

func TestHookPersistenceAndOwnEvent(t *testing.T) {
	s, home := newTestStore(t)
	e, err := s.SaveHook(HookEntry{Name: "notify", Event: "turn-end", Command: "echo hi", Agents: []string{"claude_code"}})
	if err != nil {
		t.Fatal(err)
	}
	reloaded := NewStoreWithHome(s.path, home)
	got := reloaded.Hook(e.Name)
	if got == nil || got.Command != "echo hi" || len(got.Agents) != 1 {
		t.Fatalf("重载 hook = %+v", got)
	}
	if !reloaded.DeleteHook(e.Name) || reloaded.Hook(e.Name) != nil {
		t.Fatal("删除失败")
	}

	own := HookEntry{Name: "own", Event: "claude_code:Stop", Command: "echo hi"}
	if _, _, _, err := hookTarget("claude_code", own); err != nil {
		t.Fatalf("自身事件应可用: %v", err)
	}
	if _, _, _, err := hookTarget("codex_cli", own); err == nil {
		t.Fatal("别的 Agent 的自身事件应报错")
	}

}

func TestHookAgentEvents(t *testing.T) {
	if len(HookAgentEvents("claude_code")) == 0 || len(HookAgentEvents("gemini_cli")) == 0 {
		t.Fatal("应返回 Agent 自己的事件")
	}
	if HookAgentEvents("unknown") != nil {
		t.Fatal("未知 Agent 应返回 nil")
	}
}

func TestApplyHookConcurrent(t *testing.T) {
	s, home := newTestStore(t)
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		name := string(rune('a' + i))
		go func(name string) {
			done <- s.ApplyHook(HookEntry{Name: name, Event: "turn-end", Command: "echo " + name}, "claude_code")
		}(name)
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatalf("ApplyHook: %v", err)
		}
	}
	root, err := readHookRoot(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	hooks := root["hooks"].(map[string]any)
	arr := hooks["Stop"].([]any)
	if len(arr) != 8 {
		t.Fatalf("并发写入丢了条目: %d", len(arr))
	}
}
