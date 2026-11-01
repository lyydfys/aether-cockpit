package library

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"aether-cockpit/internal/core/jsoncutil"
)

// HookEntry 是资源库里的一条 Agent hook。Event 使用可移植事件
// （turn-end / after-tool / before-tool / prompt / session-start），
// 或 "<agent id>:<该 Agent 自己的事件>"。
type HookEntry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Event       string   `json:"event"`
	Matcher     string   `json:"matcher,omitempty"`
	Command     string   `json:"command"`
	Timeout     int      `json:"timeout,omitempty"`
	Agents      []string `json:"agents,omitempty"`
	CreatedAt   int64    `json:"created_at"`
}

type hookFormat struct {
	dir    []string
	events map[string]string
	native []string
	tools  map[string]string
	ms     bool
}

var hookFormats = map[string]hookFormat{
	"claude_code": {
		dir:    []string{".claude", "settings.json"},
		events: map[string]string{"turn-end": "Stop", "after-tool": "PostToolUse", "before-tool": "PreToolUse", "prompt": "UserPromptSubmit", "session-start": "SessionStart"},
		native: []string{"PreToolUse", "PostToolUse", "PostToolUseFailure", "Notification", "UserPromptSubmit", "SessionStart", "SessionEnd", "Stop", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "PermissionRequest", "PermissionDenied"},
		tools:  map[string]string{"shell": "Bash", "edit": "Edit|Write|MultiEdit"},
	},
	"codex_cli": {
		dir:    []string{".codex", "hooks.json"},
		events: map[string]string{"turn-end": "Stop", "after-tool": "PostToolUse", "before-tool": "PreToolUse", "prompt": "UserPromptSubmit", "session-start": "SessionStart"},
		native: []string{"PreToolUse", "PermissionRequest", "PostToolUse", "PreCompact", "PostCompact", "SessionStart", "SessionEnd", "UserPromptSubmit", "SubagentStart", "SubagentStop", "Stop", "Interrupt"},
		tools:  map[string]string{"shell": "Bash", "edit": "apply_patch"},
	},
	"gemini_cli": {
		dir:    []string{".gemini", "settings.json"},
		events: map[string]string{"turn-end": "AfterAgent", "after-tool": "AfterTool", "before-tool": "BeforeTool", "prompt": "BeforeAgent", "session-start": "SessionStart"},
		native: []string{"BeforeTool", "AfterTool", "BeforeAgent", "AfterAgent", "Notification", "SessionStart", "SessionEnd", "PreCompress", "BeforeModel", "AfterModel", "BeforeToolSelection"},
		tools:  map[string]string{"shell": "run_shell_command", "edit": "write_file|replace"},
		ms:     true,
	},
}

// HookAgents 返回可以写 hook 的 Agent id。
func HookAgents() []string { return []string{"claude_code", "codex_cli", "gemini_cli"} }

// HookAgentEvents 返回某个 Agent 自己的事件名。
func HookAgentEvents(agent string) []string {
	f, ok := hookFormats[agent]
	if !ok {
		return nil
	}
	return append([]string(nil), f.native...)
}

func HookAgentSupported(agent string) bool {
	_, ok := hookFormats[agent]
	return ok
}

var hookNameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// HooksList 返回所有 hook 的拷贝。
func (s *Store) HooksList() []HookEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]HookEntry, len(s.Hooks))
	for i, h := range s.Hooks {
		cp := h
		cp.Agents = append([]string(nil), h.Agents...)
		out[i] = cp
	}
	return out
}

// Hook 按名字取一条 hook 的拷贝。
func (s *Store) Hook(name string) *HookEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Hooks {
		if s.Hooks[i].Name == name {
			cp := s.Hooks[i]
			cp.Agents = append([]string(nil), s.Hooks[i].Agents...)
			return &cp
		}
	}
	return nil
}

// SaveHook 保存 hook（按名字 upsert）。
func (s *Store) SaveHook(e HookEntry) (HookEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e.Name = strings.TrimSpace(e.Name)
	e.Event = strings.TrimSpace(e.Event)
	e.Command = strings.TrimSpace(e.Command)
	if !hookNameRE.MatchString(e.Name) {
		return e, fmt.Errorf("hook 名字只能含字母、数字、- 和 _")
	}
	if e.Event == "" {
		return e, fmt.Errorf("缺少事件")
	}
	if e.Command == "" {
		return e, fmt.Errorf("缺少命令")
	}
	if len(e.Description) > 200 {
		e.Description = e.Description[:200]
	}
	for i := range s.Hooks {
		if s.Hooks[i].Name == e.Name {
			e.ID = s.Hooks[i].ID
			e.CreatedAt = s.Hooks[i].CreatedAt
			s.Hooks[i] = e
			if err := s.flushLocked(); err != nil {
				return e, err
			}
			return e, nil
		}
	}
	if e.ID == "" {
		e.ID = newID(e.Name, func(id string) int {
			for i := range s.Hooks {
				if s.Hooks[i].ID == id {
					return i
				}
			}
			return -1
		})
	}
	if e.CreatedAt == 0 {
		e.CreatedAt = time.Now().Unix()
	}
	s.Hooks = append(s.Hooks, e)
	if err := s.flushLocked(); err != nil {
		return e, err
	}
	return e, nil
}

// DeleteHook 按名字删除 hook。
func (s *Store) DeleteHook(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Hooks {
		if s.Hooks[i].Name == name {
			s.Hooks = append(s.Hooks[:i], s.Hooks[i+1:]...)
			s.flushLockedAndLog()
			return true
		}
	}
	return false
}

// hookTarget 把可移植事件/工具换成 Agent 自己的名字。
func hookTarget(agent string, h HookEntry) (event, matcher string, timeout int, err error) {
	f, ok := hookFormats[agent]
	if !ok {
		return "", "", 0, fmt.Errorf("%s 不支持 hooks", agent)
	}
	if a, ev, ok := strings.Cut(h.Event, ":"); ok {
		if a != agent {
			return "", "", 0, fmt.Errorf("%s 是别的 Agent 的事件", h.Event)
		}
		found := false
		for _, n := range f.native {
			if n == ev {
				found = true
				break
			}
		}
		if !found {
			return "", "", 0, fmt.Errorf("%s 没有事件 %s", agent, ev)
		}
		event, matcher = ev, h.Matcher
	} else {
		event = f.events[h.Event]
		if event == "" {
			return "", "", 0, fmt.Errorf("未知事件: %s", h.Event)
		}
		matcher = f.tools[h.Matcher]
	}
	timeout = h.Timeout
	if f.ms {
		timeout *= 1000
	}
	return event, matcher, timeout, nil
}

func hookEntryValue(matcher, command string, timeout int) map[string]any {
	cmd := map[string]any{"type": "command", "command": command}
	if timeout > 0 {
		cmd["timeout"] = timeout
	}
	e := map[string]any{"hooks": []any{cmd}}
	if matcher != "" {
		e["matcher"] = matcher
	}
	return e
}

func hookEntryMatches(v any, matcher, command string, timeout int) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	gotMatcher, _ := m["matcher"].(string)
	if gotMatcher != matcher {
		return false
	}
	arr, _ := m["hooks"].([]any)
	for _, x := range arr {
		hm, ok := x.(map[string]any)
		if !ok {
			continue
		}
		if c, _ := hm["command"].(string); c != command {
			continue
		}
		ct := 0
		switch t := hm["timeout"].(type) {
		case float64:
			ct = int(t)
		case int:
			ct = t
		}
		if ct == timeout {
			return true
		}
	}
	return false
}

func readHookRoot(path string) (map[string]any, error) {
	root := map[string]any{}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return root, nil
		}
		return nil, err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return root, nil
	}
	normalized, err := jsoncutil.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s 不是合法 JSONC: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(normalized, &root); err != nil {
		return nil, err
	}
	return root, nil
}

func (s *Store) writeHookRoot(path string, root map[string]any) error {
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, err := jsoncutil.SpliceMember(raw, "hooks", root["hooks"])
		if err != nil {
			return err
		}
		if err := s.backupFile(path); err != nil {
			return fmt.Errorf("备份失败: %w", err)
		}
		return atomicWrite(path, out)
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'))
}

// ApplyHook 把 hook 写进某个 Agent 的 hooks 文件（幂等：先删除同一条旧条目）。
func (s *Store) ApplyHook(h HookEntry, agent string) error {
	return s.ApplyHookTo(s.home, h, agent)
}

// ApplyHookTo 与 ApplyHook 相同，但写到指定家目录（WSL 发行版等）。
func (s *Store) ApplyHookTo(home string, h HookEntry, agent string) error {
	s.hookMu.Lock()
	defer s.hookMu.Unlock()
	if home == "" {
		return errNoHome
	}
	f, ok := hookFormats[agent]
	if !ok {
		return fmt.Errorf("%s 不支持 hooks", agent)
	}
	event, matcher, timeout, err := hookTarget(agent, h)
	if err != nil {
		return err
	}
	path := filepath.Join(append([]string{home}, f.dir...)...)
	root, err := readHookRoot(path)
	if err != nil {
		return err
	}
	hooks, _ := root["hooks"].(map[string]any)
	if v, exists := root["hooks"]; exists {
		if _, ok := v.(map[string]any); !ok {
			return fmt.Errorf("%s 里的 hooks 不是对象，拒绝改写", filepath.Base(path))
		}
	}
	if hooks == nil {
		hooks = map[string]any{}
	}
	arr, _ := hooks[event].([]any)
	filtered := make([]any, 0, len(arr)+1)
	for _, e := range arr {
		if hookEntryMatches(e, matcher, h.Command, timeout) {
			continue
		}
		filtered = append(filtered, e)
	}
	filtered = append(filtered, hookEntryValue(matcher, h.Command, timeout))
	hooks[event] = filtered
	root["hooks"] = hooks
	return s.writeHookRoot(path, root)
}

// RemoveHook 从某个 Agent 的文件里删掉这条 hook，保留其他条目。
func (s *Store) RemoveHook(h HookEntry, agent string) error {
	return s.RemoveHookTo(s.home, h, agent)
}

// RemoveHookTo 与 RemoveHook 相同，但从指定家目录移除。
func (s *Store) RemoveHookTo(home string, h HookEntry, agent string) error {
	s.hookMu.Lock()
	defer s.hookMu.Unlock()
	if home == "" {
		return errNoHome
	}
	f, ok := hookFormats[agent]
	if !ok {
		return fmt.Errorf("%s 不支持 hooks", agent)
	}
	event, matcher, timeout, err := hookTarget(agent, h)
	if err != nil {
		return err
	}
	path := filepath.Join(append([]string{home}, f.dir...)...)
	root, err := readHookRoot(path)
	if err != nil {
		return err
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		return nil
	}
	arr, _ := hooks[event].([]any)
	filtered := make([]any, 0, len(arr))
	changed := false
	for _, e := range arr {
		if hookEntryMatches(e, matcher, h.Command, timeout) {
			changed = true
			continue
		}
		filtered = append(filtered, e)
	}
	if !changed {
		return nil
	}
	if len(filtered) == 0 {
		delete(hooks, event)
	} else {
		hooks[event] = filtered
	}
	root["hooks"] = hooks
	return s.writeHookRoot(path, root)
}
