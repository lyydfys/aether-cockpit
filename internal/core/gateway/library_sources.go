package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// 本机资源来源发现。
//
// 各 Agent 的加载规则（也与相关项目的实现一致）：
//   - Claude Code：~/.claude/skills、~/.claude/agents；安装的插件来自
//     ~/.claude/plugins/installed_plugins.json，插件可自带 skills/agents。
//   - Codex：~/.codex/skills（.system 为内置）、~/.codex/agents；只有
//     config.toml 里 enabled=true 的插件才扫描；市场缓存不算本机资源。
//   - Qoder / WorkBuddy：~/.qoder/skills、~/.workbuddy/skills；
//     .qoder/settings.json 的 enabledPlugins 决定插件；mcpServers 在
//     .qoder/settings.json。
//   - Gemini CLI：~/.gemini/config/plugins/* 下已安装插件的 skills。
//   - Cursor：~/.cursor/skills-cursor。
//
// 只扫用户目录与“已启用/已安装”的插件；Codex 的 .tmp 市场缓存与
// WorkBuddy 的 connectors-marketplace 是商店目录，不作为本机资源展示。

type resourceRoot struct {
	Dir    string
	Agents []string
	Kind   string // skill / agent；agent 根另带 md/toml 扩展名
	Ext    string
	Source string // user / plugin / builtin / project
	Label  string
	Plugin string
}

type pluginRoot struct {
	Name  string
	Dir   string
	Label string
	Agent string
}

func skillRoots(home string, projects []LibraryProject) []resourceRoot {
	out := make([]resourceRoot, 0, 32)
	add := func(dir string, agents []string, source, label, plugin string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		out = append(out, resourceRoot{Dir: dir, Agents: agents, Kind: "skill", Source: source, Label: label, Plugin: plugin})
	}
	if home != "" {
		add(filepath.Join(home, ".claude", "skills"), []string{"claude_code"}, "user", "Claude Code 用户", "")
		add(filepath.Join(home, ".agents", "skills"), []string{"codex_cli", "gemini_cli", "opencode"}, "user", "Agents 通用", "")
		add(filepath.Join(home, ".qoder", "skills"), []string{"qoder"}, "user", "Qoder 用户", "")
		add(filepath.Join(home, ".qoder-cn", "skills"), []string{"qoder"}, "user", "Qoder CN 用户", "")
		add(filepath.Join(home, ".workbuddy", "skills"), []string{"workbuddy"}, "user", "WorkBuddy 用户", "")
		add(filepath.Join(home, ".workbuddy-ai", "skills"), []string{"workbuddy"}, "user", "WorkBuddy AI 用户", "")
		add(filepath.Join(home, ".zcode", "skills"), []string{"zcode"}, "user", "ZCode 用户", "")
		add(filepath.Join(home, ".trae-cn", "skills"), []string{"trae"}, "user", "Trae CN 用户", "")
		add(filepath.Join(home, ".trae", "skills"), []string{"trae"}, "user", "Trae 用户", "")
		add(filepath.Join(home, ".codebuddy", "skills"), []string{"codebuddy"}, "user", "CodeBuddy 用户", "")
		add(filepath.Join(home, ".codebuddycn", "skills"), []string{"codebuddy"}, "user", "CodeBuddy CN 用户", "")
		add(filepath.Join(home, ".cursor", "skills-cursor"), []string{"cursor"}, "user", "Cursor 用户", "")
		add(filepath.Join(home, ".codex", "skills"), []string{"codex_cli"}, "user", "Codex 用户", "")
		// .system 是 Codex 自带技能，单独标出来。
		add(filepath.Join(home, ".codex", "skills", ".system"), []string{"codex_cli"}, "builtin", "Codex 内置", "")
		add(filepath.Join(home, ".gemini", "skills"), []string{"gemini_cli"}, "user", "Gemini CLI 用户", "")
		// 与 本机配置库 等项目管理器对齐：这些 Agent 也使用标准 SKILL.md 目录。
		add(filepath.Join(home, ".config", "opencode", "skills"), []string{"opencode"}, "user", "opencode 用户", "")
		add(filepath.Join(home, ".gemini", "config", "skills"), []string{"gemini_cli"}, "user", "Gemini 配置", "")
		add(filepath.Join(home, ".claude-desktop", "skills"), []string{"claude_desktop"}, "user", "Claude Desktop 用户", "")
		add(filepath.Join(home, ".grok", "skills"), []string{"grok"}, "user", "Grok Build 用户", "")
		add(filepath.Join(home, ".kimi-code", "skills"), []string{"kimi"}, "user", "Kimi Code 用户", "")
		add(filepath.Join(home, ".dsh", "skills"), []string{"dsh"}, "user", "DSH 用户", "")
		add(filepath.Join(home, ".minimax", "skills"), []string{"mimocode"}, "user", "MiMo Code 用户", "")
		add(filepath.Join(home, ".openclaw", "skills"), []string{"openclaw"}, "user", "OpenClaw 用户", "")
		add(filepath.Join(home, ".pi", "agent", "skills"), []string{"pi"}, "user", "Pi 用户", "")
		if la := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); la != "" {
			add(filepath.Join(la, "hermes", "skills"), []string{"hermes"}, "user", "Hermes 用户", "")
		}
		add(filepath.Join(home, ".hermes", "skills"), []string{"hermes"}, "user", "Hermes 用户", "")
		for _, p := range discoverPluginRoots(home) {
			if dir := pluginSubdir(p.Dir, "skills"); dir != "" {
				add(dir, []string{p.Agent}, "plugin", p.Label, p.Name)
			}
		}
	}
	for _, proj := range projects {
		if strings.TrimSpace(proj.Dir) == "" {
			continue
		}
		add(filepath.Join(proj.Dir, ".claude", "skills"), []string{"claude_code"}, "project", "项目 · Claude Code", "")
		add(filepath.Join(proj.Dir, ".agents", "skills"), []string{"codex_cli", "gemini_cli", "opencode"}, "project", "项目 · Agents", "")
		add(filepath.Join(proj.Dir, ".qoder", "skills"), []string{"qoder"}, "project", "项目 · Qoder", "")
		add(filepath.Join(proj.Dir, ".workbuddy", "skills"), []string{"workbuddy"}, "project", "项目 · WorkBuddy", "")
		add(filepath.Join(proj.Dir, ".cursor", "skills-cursor"), []string{"cursor"}, "project", "项目 · Cursor", "")
		add(filepath.Join(proj.Dir, ".codex", "skills"), []string{"codex_cli"}, "project", "项目 · Codex", "")
		add(filepath.Join(proj.Dir, ".opencode", "skills"), []string{"opencode"}, "project", "项目 · opencode", "")
		add(filepath.Join(proj.Dir, ".qoder", "skills"), []string{"qoder"}, "project", "项目 · Qoder", "")
		add(filepath.Join(proj.Dir, ".qoder-cn", "skills"), []string{"qoder"}, "project", "项目 · Qoder CN", "")
		add(filepath.Join(proj.Dir, ".zcode", "skills"), []string{"zcode"}, "project", "项目 · ZCode", "")
		add(filepath.Join(proj.Dir, ".trae-cn", "skills"), []string{"trae"}, "project", "项目 · Trae CN", "")
		add(filepath.Join(proj.Dir, ".workbuddy", "skills"), []string{"workbuddy"}, "project", "项目 · WorkBuddy", "")
		add(filepath.Join(proj.Dir, ".grok", "skills"), []string{"grok"}, "project", "项目 · Grok Build", "")
		add(filepath.Join(proj.Dir, ".kimi-code", "skills"), []string{"kimi"}, "project", "项目 · Kimi Code", "")
		add(filepath.Join(proj.Dir, ".minimax", "skills"), []string{"mimocode"}, "project", "项目 · MiMo Code", "")
	}
	return out
}

func agentRoots(home string, projects []LibraryProject) []resourceRoot {
	out := make([]resourceRoot, 0, 20)
	add := func(dir, agent, ext, source, label, plugin string) {
		if strings.TrimSpace(dir) == "" {
			return
		}
		out = append(out, resourceRoot{Dir: dir, Agents: []string{agent}, Kind: "agent", Ext: ext, Source: source, Label: label, Plugin: plugin})
	}
	if home != "" {
		add(filepath.Join(home, ".claude", "agents"), "claude_code", "md", "user", "Claude Code 用户", "")
		add(filepath.Join(home, ".codex", "agents"), "codex_cli", "toml", "user", "Codex 用户", "")
		add(filepath.Join(home, ".config", "opencode", "agent"), "opencode", "md", "user", "opencode 用户", "")
		add(filepath.Join(home, ".config", "mimocode", "agent"), "mimocode", "md", "user", "MiMo Code 用户", "")
		add(filepath.Join(home, ".claude-desktop", "agents"), "claude_desktop", "md", "user", "Claude Desktop 用户", "")
		add(filepath.Join(home, ".grok", "agents"), "grok", "md", "user", "Grok Build 用户", "")
		add(filepath.Join(home, ".kimi-code", "agents"), "kimi", "md", "user", "Kimi Code 用户", "")
		add(filepath.Join(home, ".dsh", "agents"), "dsh", "md", "user", "DSH 用户", "")
		add(filepath.Join(home, ".openclaw", "agents"), "openclaw", "md", "user", "OpenClaw 用户", "")
		add(filepath.Join(home, ".pi", "agent", "agents"), "pi", "md", "user", "Pi 用户", "")
		add(filepath.Join(home, ".qoder", "agents"), "qoder", "md", "user", "Qoder 用户", "")
		add(filepath.Join(home, ".qoder-cn", "agents"), "qoder", "md", "user", "Qoder CN 用户", "")
		add(filepath.Join(home, ".zcode", "agents"), "zcode", "md", "user", "ZCode 用户", "")
		add(filepath.Join(home, ".trae-cn", "agents"), "trae", "md", "user", "Trae CN 用户", "")
		add(filepath.Join(home, ".workbuddy-ai", "agents"), "workbuddy", "md", "user", "WorkBuddy AI 用户", "")
		add(filepath.Join(home, ".codebuddy", "agents"), "codebuddy", "md", "user", "CodeBuddy 用户", "")
		for _, p := range discoverPluginRoots(home) {
			if dir := pluginSubdir(p.Dir, "agents"); dir != "" {
				add(dir, p.Agent, "", "plugin", p.Label, p.Name)
			}
		}
	}
	for _, proj := range projects {
		if strings.TrimSpace(proj.Dir) == "" {
			continue
		}
		add(filepath.Join(proj.Dir, ".claude", "agents"), "claude_code", "md", "project", "项目 · Claude Code", "")
		add(filepath.Join(proj.Dir, ".codex", "agents"), "codex_cli", "toml", "project", "项目 · Codex", "")
		add(filepath.Join(proj.Dir, ".opencode", "agent"), "opencode", "md", "project", "项目 · opencode", "")
	}
	return out
}

// discoverPluginRoots 汇总“已启用/已安装”的插件目录。
func discoverPluginRoots(home string) []pluginRoot {
	if home == "" {
		return nil
	}
	out := make([]pluginRoot, 0, 16)
	out = append(out, qoderPluginRoots(home)...)
	out = append(out, claudePluginRoots(home)...)
	out = append(out, codexPluginRoots(home)...)
	out = append(out, geminiPluginRoots(home)...)
	out = append(out, workBuddyPluginRoots(home)...)
	out = append(out, traePluginRoots(home)...)
	return out
}

func qoderPluginRoots(home string) []pluginRoot {
	out := qoderPluginRootsAt(filepath.Join(home, ".qoder"), "Qoder 插件 · ", "qoder")
	out = append(out, qoderPluginRootsAt(filepath.Join(home, ".qoder-cn"), "Qoder CN 插件 · ", "qoder")...)
	return out
}

// qoderPluginRootsAt 优先读 installed_plugins_v2.json 的 installPath（含 enabled），
// 旧版本没有该文件时按 enabledPlugins + cache 目录回退。
func qoderPluginRootsAt(root, labelPrefix, agent string) []pluginRoot {
	pluginsDir := filepath.Join(root, "plugins")
	var out []pluginRoot
	seen := map[string]bool{}
	addRoot := func(name, dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[strings.ToLower(dir)] {
			return
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return
		}
		seen[strings.ToLower(dir)] = true
		out = append(out, pluginRoot{Name: name, Dir: dir, Label: labelPrefix + name, Agent: agent})
	}
	if raw, err := os.ReadFile(filepath.Join(pluginsDir, "installed_plugins_v2.json")); err == nil {
		var d struct {
			Plugins map[string][]struct {
				InstallPath string `json:"installPath"`
				Enabled     *bool  `json:"enabled"`
			} `json:"plugins"`
		}
		if json.Unmarshal(raw, &d) == nil {
			for key, installs := range d.Plugins {
				name, _, _ := strings.Cut(key, "@")
				for _, inst := range installs {
					if inst.Enabled != nil && !*inst.Enabled {
						continue
					}
					addRoot(name, inst.InstallPath)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(pluginsDir, "installed_plugins.json")); err == nil {
		var d struct {
			Plugins map[string]struct {
				InstallPath string `json:"installPath"`
			} `json:"plugins"`
		}
		if json.Unmarshal(raw, &d) == nil {
			for key, inst := range d.Plugins {
				name, _, _ := strings.Cut(key, "@")
				addRoot(name, inst.InstallPath)
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	// 旧版：settings.enabledPlugins + cache/<market>/<name>/<version>。
	raw, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		return nil
	}
	var s struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	for key, on := range s.EnabledPlugins {
		if !on {
			continue
		}
		name, market, _ := strings.Cut(key, "@")
		for _, dir := range qoderPluginCacheDirs(filepath.Join(pluginsDir, "cache"), name, market) {
			addRoot(name, dir)
		}
	}
	return out
}

func qoderPluginCacheDirs(base, name, market string) []string {
	patterns := []string{filepath.Join(base, "*", name, "*"), filepath.Join(base, name, "*")}
	if market != "" {
		patterns = append([]string{filepath.Join(base, market, name, "*")}, patterns...)
	}
	seen := map[string]bool{}
	var out []string
	for _, pat := range patterns {
		matches, _ := filepath.Glob(pat)
		for _, dir := range matches {
			if seen[dir] {
				continue
			}
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				seen[dir] = true
				out = append(out, dir)
			}
		}
	}
	return out
}

func claudePluginRoots(home string) []pluginRoot {
	path := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var d struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return nil
	}
	out := make([]pluginRoot, 0, len(d.Plugins))
	for key, installs := range d.Plugins {
		if len(installs) == 0 || strings.TrimSpace(installs[0].InstallPath) == "" {
			continue
		}
		name, _, _ := strings.Cut(key, "@")
		dir := installs[0].InstallPath
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			continue
		}
		out = append(out, pluginRoot{Name: name, Dir: dir, Label: "Claude 插件 · " + name, Agent: "claude_code"})
	}
	return out
}

func codexPluginRoots(home string) []pluginRoot {
	path := filepath.Join(home, ".codex", "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]any
	if toml.Unmarshal(raw, &doc) != nil {
		return nil
	}
	plugins, _ := doc["plugins"].(map[string]any)
	out := make([]pluginRoot, 0, len(plugins))
	for key, val := range plugins {
		m, _ := val.(map[string]any)
		on, _ := m["enabled"].(bool)
		if !on {
			continue
		}
		name, market, _ := strings.Cut(key, "@")
		for _, dir := range codexPluginDirs(home, name, market) {
			out = append(out, pluginRoot{Name: name, Dir: dir, Label: "Codex 插件 · " + name, Agent: "codex_cli"})
		}
	}
	return out
}

func codexPluginDirs(home, name, market string) []string {
	var candidates []string
	cache := filepath.Join(home, ".codex", "plugins", "cache")
	switch market {
	case "openai-bundled":
		candidates = append(candidates,
			filepath.Join(cache, "openai-bundled", name),
			filepath.Join(home, ".codex", ".tmp", "bundled-marketplaces", "openai-bundled", "plugins", name),
		)
	case "openai-primary-runtime":
		candidates = append(candidates,
			filepath.Join(home, ".cache", "codex-runtimes", "codex-primary-runtime", "plugins", "openai-primary-runtime", "plugins", name),
		)
	default:
		if market != "" {
			candidates = append(candidates, filepath.Join(cache, market, name))
		}
		candidates = append(candidates, filepath.Join(cache, name))
	}
	out := []string{}
	seen := map[string]bool{}
	for _, dir := range candidates {
		if seen[dir] {
			continue
		}
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}

func geminiPluginRoots(home string) []pluginRoot {
	base := filepath.Join(home, ".gemini", "config", "plugins")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	out := make([]pluginRoot, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		out = append(out, pluginRoot{Name: name, Dir: filepath.Join(base, name), Label: "Gemini 插件 · " + name, Agent: "gemini_cli"})
	}
	return out
}

func workBuddyPluginRoots(home string) []pluginRoot {
	var out []pluginRoot
	for _, spec := range []struct{ base, label, agent string }{
		{".workbuddy", "WorkBuddy 插件 · ", "workbuddy"},
		{".workbuddy-ai", "WorkBuddy AI 插件 · ", "workbuddy"},
	} {
		root := filepath.Join(home, spec.base)
		raw, err := os.ReadFile(filepath.Join(root, "settings.json"))
		if err != nil {
			continue
		}
		var settings struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if json.Unmarshal(raw, &settings) != nil {
			continue
		}
		instRaw, err := os.ReadFile(filepath.Join(root, "plugins", "installed_plugins.json"))
		if err != nil {
			continue
		}
		var inst struct {
			Plugins map[string][]struct {
				InstallPath string `json:"installPath"`
			} `json:"plugins"`
		}
		if json.Unmarshal(instRaw, &inst) != nil {
			continue
		}
		for key, installs := range inst.Plugins {
			if on, ok := settings.EnabledPlugins[key]; ok && !on {
				continue
			}
			name, _, _ := strings.Cut(key, "@")
			for _, item := range installs {
				if st, err := os.Stat(item.InstallPath); err != nil || !st.IsDir() {
					continue
				}
				out = append(out, pluginRoot{Name: name, Dir: item.InstallPath, Label: spec.label + name, Agent: spec.agent})
			}
		}
	}
	return out
}

func traePluginRoots(home string) []pluginRoot {
	var out []pluginRoot
	for _, base := range []string{
		filepath.Join(home, ".trae-cn", "plugins", "trae-remote-official"),
		filepath.Join(home, ".trae", "plugins", "trae-remote-official"),
	} {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			out = append(out, pluginRoot{Name: e.Name(), Dir: filepath.Join(base, e.Name()), Label: "Trae 插件 · " + e.Name(), Agent: "trae"})
		}
	}
	return out
}

// pluginSubdir 找到插件的 skills/agents 目录；缓存目录可能多一层版本号。
func pluginSubdir(dir, sub string) string {
	if st, err := os.Stat(filepath.Join(dir, sub)); err == nil && st.IsDir() {
		return filepath.Join(dir, sub)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	versions := make([]string, 0, 4)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, e.Name(), sub)); err == nil && st.IsDir() {
			versions = append(versions, e.Name())
		}
	}
	if len(versions) == 0 {
		return ""
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	return filepath.Join(dir, versions[0], sub)
}
