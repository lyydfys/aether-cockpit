package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"aether-cockpit/internal/core/library"

	toml "github.com/pelletier/go-toml/v2"
)

// 本机已有资源的只读发现：指令文件、子代理定义。
// 技能与 MCP 的发现分别在 library_found.go / library_projects.go 里。
//
// 所有这些函数只读用户本机文件：导入动作只写库，不移动也不删除原文件。

// maxLocalFileBytes 是单个本机文件的读取上限。指令与子代理正文是给人看
// 的文本，超过这个量级说明不是普通配置，截断而不是把整份内容塞进响应。
const maxLocalFileBytes = 256 << 10

// readLocalText 读取本机文本文件。
func readLocalText(path string) (text string, size, modified int64, exists, truncated bool) {
	if path == "" {
		return "", 0, 0, false, false
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return "", 0, 0, false, false
	}
	size, modified, exists = st.Size(), st.ModTime().Unix(), true
	f, err := os.Open(path)
	if err != nil {
		return "", size, modified, true, false
	}
	defer f.Close()
	// 只读上限 +1 字节：既能判断是否超限，又不会为超大文件分配整份内存。
	raw, err := io.ReadAll(io.LimitReader(f, maxLocalFileBytes+1))
	if err != nil {
		return "", size, modified, true, false
	}
	if len(raw) > maxLocalFileBytes {
		raw = raw[:maxLocalFileBytes]
		// 截断点可能落在多字节字符中间；最多回退 3 个字节即到合法边界。
		for i := 0; i < 4 && len(raw) > 0 && !utf8.Valid(raw); i++ {
			raw = raw[:len(raw)-1]
		}
		truncated = true
	}
	return string(raw), size, modified, true, truncated
}

// ---- 子代理定义发现 ----

type foundSubagent struct {
	Name        string
	Description string
	Content     string
	Model       string
	Tools       string
	Path        string
	Agents      []string
	Sources     []string
	Plugin      string
}

func (f *foundSubagent) addSource(label string) {
	if label == "" {
		return
	}
	for _, s := range f.Sources {
		if s == label {
			return
		}
	}
	f.Sources = append(f.Sources, label)
}

// parseFrontmatter 解析 markdown 的 YAML frontmatter。
//
// 只支持子代理定义里会用到的扁平键值：单行标量、以及缩进的 "- 值" 列表。
// 不引入 YAML 依赖——这里读到的内容只用于展示和复制，复杂结构交给原文。
func parseFrontmatter(raw string) (map[string]string, string) {
	fields := map[string]string{}
	t := strings.TrimPrefix(raw, "\ufeff")
	if !strings.HasPrefix(strings.TrimSpace(t), "---") {
		return fields, t
	}
	text := t[strings.Index(t, "---"):]
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return fields, t
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return fields, t
	}
	key := ""
	for _, line := range lines[1:end] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && key != "" {
			item := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")), `"'`)
			if fields[key] == "" {
				fields[key] = item
			} else {
				fields[key] += ", " + item
			}
			continue
		}
		k, v, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(k)
		fields[key] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return fields, strings.TrimPrefix(strings.Join(lines[end+1:], "\n"), "\n")
}

func collectAgentMarkdown(dir, agent string, known map[string]bool, add func(name, desc, content, model, tools, path, agent string)) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for i, e := range entries {
		if i >= 200 || e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		if info, err := e.Info(); err != nil || info.Size() > maxLocalFileBytes {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fields, body := parseFrontmatter(string(raw))
		name := strings.TrimSpace(fields["name"])
		if name == "" {
			name = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		}
		if known[name] {
			continue
		}
		add(name, fields["description"], strings.TrimSpace(body), fields["model"], fields["tools"], path, agent)
	}
}

func collectCodexAgents(dir string, known map[string]bool, add func(name, desc, content, model, tools, path, agent string)) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for i, e := range entries {
		if i >= 200 || e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".toml") {
			continue
		}
		if info, err := e.Info(); err != nil || info.Size() > maxLocalFileBytes {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc struct {
			Name                  string `toml:"name"`
			Description           string `toml:"description"`
			Model                 string `toml:"model"`
			DeveloperInstructions string `toml:"developer_instructions"`
			Instructions          string `toml:"instructions"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			continue
		}
		name := strings.TrimSpace(doc.Name)
		if name == "" {
			name = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		}
		if known[name] {
			continue
		}
		content := strings.TrimSpace(doc.DeveloperInstructions)
		if content == "" {
			content = strings.TrimSpace(doc.Instructions)
		}
		add(name, doc.Description, content, doc.Model, "", path, "codex_cli")
	}
}

// discoverSubagents 扫描各 Agent 已有的子代理定义。
// Claude Code 用 <name>.md（frontmatter + 正文），Codex 用 <name>.toml
// （developer_instructions），opencode 与其同源分支用 agent/*.md。
func discoverSubagents(home string, projects []LibraryProject, known map[string]bool) []foundSubagent {
	byName := map[string]*foundSubagent{}
	add := func(name, desc, content, model, tools, path, agent, label, plugin string) {
		name = strings.TrimSpace(name)
		if name == "" || known[name] {
			return
		}
		// 没有正文的 md/toml 不是可用的子代理定义，收进库也会被 SaveAgent
		// 拒绝；这里直接跳过，避免页面上出现导入必然失败的条目。
		if strings.TrimSpace(content) == "" {
			return
		}
		if f, ok := byName[name]; ok {
			if !hasAgent(f.Agents, agent) {
				f.Agents = append(f.Agents, agent)
			}
			f.addSource(label)
			if f.Plugin == "" {
				f.Plugin = plugin
			}
			if f.Model == "" {
				f.Model = model
			}
			if f.Tools == "" {
				f.Tools = tools
			}
			return
		}
		byName[name] = &foundSubagent{
			Name: name, Description: strings.Join(strings.Fields(desc), " "),
			Content: content, Model: model, Tools: tools, Path: path,
			Agents: []string{agent}, Sources: []string{label}, Plugin: plugin,
		}
	}

	for _, root := range agentRoots(home, projects) {
		label, plugin := root.Label, root.Plugin
		agent := ""
		if len(root.Agents) > 0 {
			agent = root.Agents[0]
		}
		addWithSource := func(name, desc, content, model, tools, path, ag string) {
			add(name, desc, content, model, tools, path, ag, label, plugin)
		}
		if root.Ext == "toml" {
			collectCodexAgents(root.Dir, known, addWithSource)
			continue
		}
		collectAgentMarkdown(root.Dir, agent, known, addWithSource)
		if root.Ext == "" {
			// 插件 agents 目录可能混用 md / toml。
			collectCodexAgents(root.Dir, known, addWithSource)
		}
	}

	out := make([]foundSubagent, 0, len(byName))
	for _, f := range byName {
		sort.Strings(f.Agents)
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (g *Gateway) foundSubagentsView() []any {
	known := map[string]bool{}
	for _, a := range g.library.List().Agents {
		known[a.Name] = true
	}
	found := discoverSubagents(g.library.Home(), g.libraryScanProjects(), known)
	out := make([]any, 0, len(found))
	for _, f := range found {
		preview, truncated := previewText(f.Content, 2<<10)
		out = append(out, map[string]any{
			"name": f.Name, "description": f.Description, "prompt": preview,
			"truncated": truncated,
			"model":     f.Model, "tools": f.Tools, "agents": f.Agents, "path": f.Path,
			"source": strings.Join(f.Sources, " · "), "sources": f.Sources, "plugin": f.Plugin,
		})
	}
	return out
}

func (g *Gateway) importFoundSubagent(name string) (library.AgentEntry, error) {
	for _, f := range discoverSubagents(g.library.Home(), g.libraryScanProjects(), map[string]bool{}) {
		if f.Name == name {
			return g.library.SaveAgent(library.AgentEntry{
				Name: f.Name, Description: f.Description,
				Tools: f.Tools, Model: f.Model, Content: f.Content,
			})
		}
	}
	return library.AgentEntry{}, os.ErrNotExist
}

// handleFoundSubagentAction POST /api/library/subagents/{action}
//
// 动作：import / import-all。只把本机定义收进库，原文件保持不动。
func (g *Gateway) handleFoundSubagentAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var body struct {
		Name  string   `json:"name"`
		Names []string `json:"names"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	fail := func(status int, msg string) {
		writeJSON(w, status, map[string]any{"error": msg})
	}
	switch action {
	case "import":
		name := strings.TrimSpace(body.Name)
		if name == "" {
			fail(http.StatusBadRequest, "缺少子代理名")
			return
		}
		if _, err := g.importFoundSubagent(name); err != nil {
			fail(http.StatusNotFound, "本机没有这个子代理: "+name)
			return
		}
		g.writeLibraryView(w)
	case "import-all":
		results := make([]map[string]any, 0, len(body.Names))
		okN := 0
		for _, n := range body.Names {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			_, err := g.importFoundSubagent(n)
			row := map[string]any{"name": n, "ok": err == nil}
			if err != nil {
				row["error"] = err.Error()
			} else {
				okN++
			}
			results = append(results, row)
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results, "ok": okN, "total": len(results)})
	default:
		fail(http.StatusNotFound, "未知动作: "+action)
	}
}

// localInstructionDocs 返回本机已有的记忆/项目指令文件（只读）。
// 与各 Agent 的用户级指令文件一起展示，方便收录进共享指令集。
func (g *Gateway) localInstructionDocs() []any {
	home := g.library.Home()
	out := make([]any, 0, 64)
	seen := map[string]bool{}
	add := func(path, source, name string) {
		if len(out) >= 400 {
			return
		}
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || seen[path] {
			return
		}
		text, size, modified, exists, truncated := readLocalText(path)
		if !exists {
			return
		}
		seen[path] = true
		preview, previewTruncated := previewText(text, 2<<10)
		out = append(out, map[string]any{
			"agent": "", "name": name, "file": filepath.Base(path),
			"path": path, "exists": true, "size": size, "modified": modified,
			"content": preview, "truncated": truncated || previewTruncated, "source": source,
		})
	}
	addGlob := func(pattern, source, namePrefix string) {
		files, _ := filepath.Glob(pattern)
		sort.Strings(files)
		for _, path := range files {
			base := filepath.Base(path)
			add(path, source, namePrefix+" · "+base)
		}
	}
	if home != "" {
		addGlob(filepath.Join(home, ".qoder", "memory", "*.md"), "Qoder 记忆", "Qoder 记忆")
		addGlob(filepath.Join(home, ".qoder", "projects", "*", "memory", "*.md"), "Qoder 项目记忆", "Qoder 项目记忆")
		addGlob(filepath.Join(home, ".qoder-cn", "memory", "*.md"), "Qoder CN 记忆", "Qoder CN 记忆")
		addGlob(filepath.Join(home, ".qoder-cn", "projects", "*", "memory", "*.md"), "Qoder CN 项目记忆", "Qoder CN 项目记忆")
		add(filepath.Join(home, ".workbuddy", "SOUL.md"), "WorkBuddy", "WorkBuddy · SOUL.md")
		addGlob(filepath.Join(home, ".workbuddy", "user-*", "SOUL.md"), "WorkBuddy", "WorkBuddy")
		addGlob(filepath.Join(home, ".workbuddy-ai", "user-*", "SOUL.md"), "WorkBuddy AI", "WorkBuddy AI")
		// 斜杠命令 / 提示词模板：各 Agent 的本地 prompt 目录。
		addGlob(filepath.Join(home, ".claude", "commands", "*.md"), "Claude Code 命令", "Claude 命令")
		addGlob(filepath.Join(home, ".codex", "prompts", "*.md"), "Codex 提示词", "Codex 提示词")
		addGlob(filepath.Join(home, ".gemini", "commands", "*.toml"), "Gemini CLI 命令", "Gemini 命令")
		addGlob(filepath.Join(home, ".gemini", "commands", "*.md"), "Gemini CLI 命令", "Gemini 命令")
		addGlob(filepath.Join(home, ".config", "opencode", "command", "*.md"), "opencode 命令", "opencode 命令")
		addGlob(filepath.Join(home, ".config", "mimocode", "command", "*.md"), "MiMo Code 命令", "MiMo 命令")
		for _, f := range []struct{ rel, source string }{
			{".grok/AGENTS.md", "Grok Build"},
			{".openclaw/AGENTS.md", "OpenClaw"},
			{".kimi-code/AGENTS.md", "Kimi Code"},
			{".dsh/AGENTS.md", "DSH"},
			{".minimax/AGENTS.md", "MiMo Code"},
			{".gemini/config/AGENTS.md", "Gemini 配置"},
			{".hermes/SOUL.md", "Hermes"},
			{".pi/agent/AGENTS.md", "Pi"},
		} {
			path := filepath.Join(home, filepath.FromSlash(f.rel))
			add(path, f.source, f.source+" · "+filepath.Base(path))
		}
		if la := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); la != "" {
			add(filepath.Join(la, "hermes", "SOUL.md"), "Hermes", "Hermes · SOUL.md")
		}
	}
	for _, proj := range g.libraryScanProjects() {
		if strings.TrimSpace(proj.Dir) == "" {
			continue
		}
		for _, name := range []string{"CLAUDE.md", "AGENTS.md", "GEMINI.md", ".cursorrules", ".windsurfrules", ".clinerules"} {
			add(filepath.Join(proj.Dir, name), "项目 · "+proj.Name, proj.Name+" · "+name)
		}
		for _, rel := range []string{".github/copilot-instructions.md"} {
			add(filepath.Join(proj.Dir, filepath.FromSlash(rel)), "项目 · "+proj.Name, proj.Name+" · "+filepath.Base(rel))
		}
		addGlob(filepath.Join(proj.Dir, ".qoder", "rules", "*.md"), "项目 · "+proj.Name, proj.Name+" · Qoder")
		addGlob(filepath.Join(proj.Dir, ".cursor", "rules", "*.mdc"), "项目 · "+proj.Name, proj.Name+" · Cursor")
		addGlob(filepath.Join(proj.Dir, ".windsurf", "rules", "*.md"), "项目 · "+proj.Name, proj.Name+" · Windsurf")
		addGlob(filepath.Join(proj.Dir, ".github", "instructions", "*.md"), "项目 · "+proj.Name, proj.Name+" · Copilot")
		addGlob(filepath.Join(proj.Dir, ".claude", "commands", "*.md"), "项目 · "+proj.Name, proj.Name+" · Claude 命令")
		addGlob(filepath.Join(proj.Dir, ".codex", "prompts", "*.md"), "项目 · "+proj.Name, proj.Name+" · Codex 提示词")
		addGlob(filepath.Join(proj.Dir, ".gemini", "commands", "*.toml"), "项目 · "+proj.Name, proj.Name+" · Gemini 命令")
		addGlob(filepath.Join(proj.Dir, ".opencode", "command", "*.md"), "项目 · "+proj.Name, proj.Name+" · opencode 命令")
	}
	return out
}
