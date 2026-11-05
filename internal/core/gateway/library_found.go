package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"aether-cockpit/internal/core/library"
)

// 资源库「In your agents」：发现用户已添加项目里、尚未进库的技能。
// 只读用户显式添加的项目目录；导入只写库，不移动或删除项目文件。

type foundProjectSkill struct {
	Name        string
	Description string
	Content     string
	Path        string
	Agents      []string
	// Sources 记录这条资源在哪些本机来源里出现（用户目录 / 插件 / 内置）。
	Sources []string
	Plugin  string
}

// projectSkillDirAgents 是项目技能目录与读它的 Agent（取自 aether 的
// projectSkillsDirs）。
var projectSkillDirAgents = []struct {
	Rel    string
	Agents []string
}{
	{".claude/skills", []string{"claude_code"}},
	{".agents/skills", []string{"codex_cli", "gemini_cli", "opencode"}},
}

// globalSkillDirAgents 是各 Agent 自己的（非项目的）技能目录，与
// library.ApplySkill 的写入目标一致。
var globalSkillDirAgents = []struct {
	Rel    string
	Agents []string
}{
	{".claude/skills", []string{"claude_code"}},
	{".agents/skills", []string{"codex_cli", "gemini_cli", "opencode"}},
}

func skillDescription(content string) string {
	t := strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(t, "---") {
		return ""
	}
	end := strings.Index(t[3:], "\n---")
	if end < 0 {
		return ""
	}
	lines := strings.Split(t[3:3+end], "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "description:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		// YAML 块标量：description: > / >- / | / |-，正文在后续缩进行。
		if strings.HasPrefix(value, ">") || strings.HasPrefix(value, "|") || value == "" {
			var parts []string
			for _, next := range lines[i+1:] {
				if strings.TrimSpace(next) == "" {
					continue
				}
				if !strings.HasPrefix(next, " ") && !strings.HasPrefix(next, "\t") {
					break
				}
				parts = append(parts, strings.TrimSpace(next))
			}
			value = strings.Join(parts, " ")
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if len(value) > 300 {
			value = value[:300] + "…"
		}
		return value
	}
	return ""
}

func collectSkillDir(root string, agents []string, known map[string]bool, byName map[string]*foundProjectSkill, label, plugin string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for i, e := range entries {
		if i >= 500 || !e.IsDir() || known[e.Name()] {
			continue
		}
		path := filepath.Join(root, e.Name(), "SKILL.md")
		info, err := os.Stat(path)
		if err != nil || info.IsDir() || info.Size() > 1<<20 {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		f, ok := byName[e.Name()]
		if !ok {
			f = &foundProjectSkill{
				Name:        e.Name(),
				Content:     string(raw),
				Description: skillDescription(string(raw)),
				Path:        path,
				Sources:     []string{label},
				Plugin:      plugin,
			}
			byName[e.Name()] = f
		} else {
			f.addSource(label)
			if f.Plugin == "" {
				f.Plugin = plugin
			}
		}
		for _, a := range agents {
			if !hasAgent(f.Agents, a) {
				f.Agents = append(f.Agents, a)
			}
		}
	}
}

func (f *foundProjectSkill) addSource(label string) {
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

func discoverAllSkills(projects []LibraryProject, home string, known map[string]bool) []foundProjectSkill {
	byName := map[string]*foundProjectSkill{}
	for _, root := range skillRoots(home, projects) {
		collectSkillDir(root.Dir, root.Agents, known, byName, root.Label, root.Plugin)
	}
	out := make([]foundProjectSkill, 0, len(byName))
	for _, f := range byName {
		sort.Strings(f.Agents)
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// discoverProjectSkills 只看项目目录（测试与旧调用兼容）。
func discoverProjectSkills(projects []LibraryProject, known map[string]bool) []foundProjectSkill {
	return discoverAllSkills(projects, "", known)
}

func (g *Gateway) foundSkillsView() []any {
	known := map[string]bool{}
	for _, sk := range g.library.List().Skills {
		known[sk.Name] = true
	}
	found := discoverAllSkills(g.libraryScanProjects(), g.library.Home(), known)
	out := make([]any, 0, len(found))
	for _, f := range found {
		content, truncated := previewText(f.Content, 1<<10)
		out = append(out, map[string]any{
			"name": f.Name, "description": f.Description, "agents": f.Agents,
			"path": f.Path, "content": content, "truncated": truncated,
			"source": strings.Join(f.Sources, " · "), "sources": f.Sources, "plugin": f.Plugin,
		})
	}
	return out
}

// previewText 截断列表里的正文，详情抽屉会按 name 再取全文。
func previewText(text string, limit int) (string, bool) {
	if limit <= 0 || len(text) <= limit {
		return text, false
	}
	raw := []byte(text[:limit])
	for i := 0; i < 4 && len(raw) > 0 && !utf8.Valid(raw); i++ {
		raw = raw[:len(raw)-1]
	}
	return string(raw), true
}

// handleFoundContent GET /api/library/found/content?kind=skills|subagents&name=...
// 列表只带预览，详情需要完整正文时按名字重新扫描本机文件。
func (g *Gateway) handleFoundContent(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	pathArg := strings.TrimSpace(r.URL.Query().Get("path"))
	if name == "" && !(kind == "instructions" && pathArg != "") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 name 或 path"})
		return
	}
	switch kind {
	case "instructions":
		target := pathArg
		wantName := strings.TrimSpace(r.URL.Query().Get("name"))
		for _, raw := range g.localInstructionDocs() {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			path, _ := m["path"].(string)
			name, _ := m["name"].(string)
			if (target != "" && filepath.Clean(path) == filepath.Clean(target)) || (target == "" && name == wantName) {
				text, _, _, exists, _ := readLocalText(path)
				if !exists {
					break
				}
				writeJSON(w, http.StatusOK, map[string]any{"text": text, "path": path, "kind": kind})
				return
			}
		}
	case "skills":
		for _, f := range discoverAllSkills(g.libraryScanProjects(), g.library.Home(), map[string]bool{}) {
			if f.Name == name {
				writeJSON(w, http.StatusOK, map[string]any{"text": f.Content, "path": f.Path, "kind": kind})
				return
			}
		}
	case "subagents":
		for _, f := range discoverSubagents(g.library.Home(), g.libraryScanProjects(), map[string]bool{}) {
			if f.Name == name {
				writeJSON(w, http.StatusOK, map[string]any{"text": f.Content, "path": f.Path, "kind": kind})
				return
			}
		}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "kind 必须是 skills 或 subagents"})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "本机没有这个资源: " + name})
}

// importFoundProjectSkill 把发现的项目技能复制进库；项目里的文件不动。
func (g *Gateway) importFoundProjectSkill(name string) (library.SkillEntry, error) {
	for _, f := range discoverAllSkills(g.libraryScanProjects(), g.library.Home(), map[string]bool{}) {
		if f.Name == name {
			return g.library.SaveSkill(library.SkillEntry{
				Name: f.Name, Description: f.Description, Content: f.Content,
			})
		}
	}
	return library.SkillEntry{}, os.ErrNotExist
}

// removeFoundProjectSkill 把技能从用户添加的项目目录里删除，返回删除的目录数。
func (g *Gateway) removeFoundProjectSkill(name string) int {
	n := removeFoundSkillDirs(g.libraryProjects(), name)
	home := g.library.Home()
	if name == "" || home == "" {
		return n
	}
	for _, d := range globalSkillDirAgents {
		if removeSkillDir(filepath.Join(home, filepath.FromSlash(d.Rel)), name) {
			n++
		}
	}
	return n
}

// removeSkillDir 删除 root 下指定技能目录（必须含 SKILL.md）。
func removeSkillDir(root, name string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() != name {
			continue
		}
		target := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
			continue
		}
		if err := os.RemoveAll(target); err == nil {
			return true
		}
	}
	return false
}

func removeFoundSkillDirs(projects []LibraryProject, name string) int {
	if name == "" {
		return 0
	}
	removed := 0
	for _, p := range projects {
		for _, d := range projectSkillDirAgents {
			root := filepath.Join(p.Dir, filepath.FromSlash(d.Rel))
			entries, err := os.ReadDir(root)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() || e.Name() != name {
					continue
				}
				target := filepath.Join(root, e.Name())
				if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
					continue
				}
				if err := os.RemoveAll(target); err == nil {
					removed++
				}
			}
		}
	}
	return removed
}

// handleFoundSkillAction POST /api/library/skills/{action}
//
// 动作：import / import-all / remove-found / remove-found-some。
func (g *Gateway) handleFoundSkillAction(w http.ResponseWriter, r *http.Request) {
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
			fail(http.StatusBadRequest, "缺少技能名")
			return
		}
		if _, err := g.importFoundProjectSkill(name); err != nil {
			fail(http.StatusNotFound, "项目里没有这个技能: "+name)
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
			_, err := g.importFoundProjectSkill(n)
			row := map[string]any{"name": n, "ok": err == nil}
			if err != nil {
				row["error"] = err.Error()
			} else {
				okN++
			}
			results = append(results, row)
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results, "ok": okN, "total": len(results)})
	case "remove-found":
		name := strings.TrimSpace(body.Name)
		if name == "" {
			fail(http.StatusBadRequest, "缺少技能名")
			return
		}
		n := g.removeFoundProjectSkill(name)
		if n == 0 {
			fail(http.StatusNotFound, "项目里没有这个技能: "+name)
			return
		}
		g.writeLibraryView(w)
	case "remove-found-some":
		removed := map[string]int{}
		for _, n := range body.Names {
			n = strings.TrimSpace(n)
			if n != "" {
				removed[n] = g.removeFoundProjectSkill(n)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
	default:
		fail(http.StatusNotFound, "未知动作: "+action)
	}
}

// ---- MCP：发现项目里各 Agent 已有的服务器 ----

type foundProjectServer struct {
	Name    string
	Config  map[string]any
	Agents  []string
	Sources []string
	Plugin  string
}

func (f *foundProjectServer) addSource(label string) {
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

// canonicalProjectServer 把各 Agent 的项目 MCP 形状折回库里的 Claude 形状。
func canonicalProjectServer(format string, m map[string]any) (map[string]any, bool) {
	switch format {
	case "claude":
		return m, true
	case "gemini":
		out := map[string]any{}
		if u, _ := m["httpUrl"].(string); u != "" {
			out["type"], out["url"] = "http", u
		} else if u, _ := m["url"].(string); u != "" {
			out["type"], out["url"] = "sse", u
		} else if cmd, _ := m["command"].(string); cmd != "" {
			out["command"] = cmd
			if a, ok := m["args"]; ok {
				out["args"] = a
			}
			if e, ok := m["env"]; ok {
				out["env"] = e
			}
		} else {
			return nil, false
		}
		if h, ok := m["headers"]; ok {
			out["headers"] = h
		}
		return out, true
	case "codex":
		out := map[string]any{}
		if u, _ := m["url"].(string); u != "" {
			out["type"], out["url"] = "http", u
			if h, ok := m["http_headers"]; ok {
				out["headers"] = h
			}
		} else if cmd, _ := m["command"].(string); cmd != "" {
			out["command"] = cmd
			if a, ok := m["args"]; ok {
				out["args"] = a
			}
			if e, ok := m["env"]; ok {
				out["env"] = e
			}
		} else {
			return nil, false
		}
		return out, true
	case "qoder":
		out := map[string]any{}
		typ := strings.ToLower(strings.TrimSpace(fmt.Sprint(m["type"])))
		switch typ {
		case "sse":
			u, _ := m["url"].(string)
			if u == "" {
				return nil, false
			}
			out["type"], out["url"] = "sse", u
		case "streamablehttp", "http":
			u, _ := m["url"].(string)
			if u == "" {
				return nil, false
			}
			out["type"], out["url"] = "http", u
		case "stdio":
			cmd, _ := m["command"].(string)
			if cmd == "" {
				return nil, false
			}
			out["command"] = cmd
		default:
			if u, _ := m["url"].(string); u != "" {
				out["type"], out["url"] = "http", u
			} else if cmd, _ := m["command"].(string); cmd != "" {
				out["command"] = cmd
			} else {
				return nil, false
			}
		}
		for _, k := range []string{"args", "env", "headers"} {
			if v, ok := m[k]; ok {
				out[k] = v
			}
		}
		return out, true
	case "opencode":
		if typ, _ := m["type"].(string); typ == "remote" {
			u, _ := m["url"].(string)
			if u == "" {
				return nil, false
			}
			out := map[string]any{"type": "http", "url": u}
			if h, ok := m["headers"]; ok {
				out["headers"] = h
			}
			return out, true
		}
		cmd, _ := m["command"].([]any)
		if len(cmd) == 0 {
			return nil, false
		}
		out := map[string]any{"command": fmt.Sprint(cmd[0])}
		if len(cmd) > 1 {
			out["args"] = cmd[1:]
		}
		if e, ok := m["environment"]; ok {
			out["env"] = e
		}
		return out, true
	}
	return nil, false
}

func discoverProjectServers(projects []LibraryProject, known map[string]bool) []foundProjectServer {
	byName := map[string]*foundProjectServer{}
	add := func(agent, name string, m map[string]any, format, source string) {
		if known[name] {
			return
		}
		if f, exists := byName[name]; exists {
			if !hasAgent(f.Agents, agent) {
				f.Agents = append(f.Agents, agent)
			}
			f.addSource(source)
			return
		}
		cfg, ok := canonicalProjectServer(format, m)
		if !ok {
			return
		}
		byName[name] = &foundProjectServer{Name: name, Config: cfg, Agents: []string{agent}, Sources: []string{source}}
	}
	for _, p := range projects {
		if root, servers, err := readMCPJSON(filepath.Join(p.Dir, ".mcp.json"), ".mcp.json"); err == nil && root != nil {
			for n, v := range servers {
				if m, ok := v.(map[string]any); ok {
					add("claude_code", n, m, "claude", "项目 · Claude Code")
				}
			}
		}
		if root, servers, err := readJSONServers(filepath.Join(p.Dir, ".gemini", "settings.json"), ".gemini/settings.json", "mcpServers"); err == nil && root != nil {
			for n, v := range servers {
				if m, ok := v.(map[string]any); ok {
					add("gemini_cli", n, m, "gemini", "项目 · Gemini CLI")
				}
			}
		}
		if _, servers, err := readCodexFile(filepath.Join(p.Dir, ".codex", "config.toml")); err == nil {
			for n, v := range servers {
				if m, ok := v.(map[string]any); ok {
					add("codex_cli", n, m, "codex", "项目 · Codex")
				}
			}
		}
		if root, servers, err := readOpenCodeServers(openCodeConfigPath(p.Dir), "opencode.json"); err == nil && root != nil {
			for n, v := range servers {
				if m, ok := v.(map[string]any); ok {
					add("opencode", n, m, "opencode", "项目 · opencode")
				}
			}
		}
		for _, item := range []struct {
			path, key, agent, source string
		}{
			{filepath.Join(p.Dir, ".cursor", "mcp.json"), "mcpServers", "cursor", "项目 · Cursor"},
			{filepath.Join(p.Dir, ".vscode", "mcp.json"), "servers", "vscode", "项目 · VS Code"},
			{filepath.Join(p.Dir, ".windsurf", "mcp_config.json"), "mcpServers", "windsurf", "项目 · Windsurf"},
			{filepath.Join(p.Dir, ".gemini", "config", "mcp_config.json"), "mcpServers", "gemini_cli", "项目 · Gemini 配置"},
			{filepath.Join(p.Dir, ".continue", "config.json"), "mcpServers", "continue", "项目 · Continue"},
		} {
			if _, servers, err := readJSONServers(item.path, item.source, item.key); err == nil {
				for n, v := range servers {
					if m, ok := v.(map[string]any); ok {
						add(item.agent, n, m, "claude", item.source)
					}
				}
			}
		}
	}
	out := make([]foundProjectServer, 0, len(byName))
	for _, f := range byName {
		sort.Strings(f.Agents)
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func mcpTransport(cfg map[string]any) string {
	if t, _ := cfg["type"].(string); t == "sse" {
		return "sse"
	}
	if u, _ := cfg["url"].(string); u != "" {
		return "http"
	}
	return "stdio"
}

func (g *Gateway) foundServersView() []any {
	known := map[string]bool{}
	for _, s := range g.library.List().MCPServers {
		known[s.Name] = true
	}
	found := discoverAllServers(g.libraryScanProjects(), g.library.Home(), known)
	// 同名服务器可能存在于多个 Agent 的配置里：合并成一行，agents 取并集，
	// 否则“发现的服务器”会重复出现多行。
	order := []string{}
	byName := map[string]*foundProjectServer{}
	for i := range found {
		f := found[i]
		if cur, ok := byName[f.Name]; ok {
			cur.Agents = dedupeStrings(append(cur.Agents, f.Agents...))
			for _, src := range f.Sources {
				cur.addSource(src)
			}
			continue
		}
		cp := f
		cp.Agents = append([]string(nil), f.Agents...)
		cp.Sources = append([]string(nil), f.Sources...)
		byName[f.Name] = &cp
		order = append(order, f.Name)
	}
	out := make([]any, 0, len(byName))
	for _, name := range order {
		f := byName[name]
		command, _ := f.Config["command"].(string)
		url, _ := f.Config["url"].(string)
		out = append(out, map[string]any{
			"server": map[string]any{
				"name": f.Name, "transport": mcpTransport(f.Config), "agents": f.Agents,
				"command": command, "args": redactServerArgs(stringArgs(f.Config["args"])),
				"url":    redactServerURL(url),
				"source": strings.Join(f.Sources, " · "), "sources": f.Sources,
			},
			"icon": "", "own": false,
		})
	}
	return out
}

// secretArgRe 匹配参数名里常见的凭据单词：发现的服务器配置只用于展示，
// 不该把 api key / token 原样带到页面上。
var secretArgRe = regexp.MustCompile(`(?i)(api[-_]?key|token|secret|password|passwd|authorization|credential)`)

// redactServerURL 去掉 URL 的 userinfo、查询串与片段（可能含密钥）。
func redactServerURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

func redactServerArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if k, _, ok := strings.Cut(a, "="); ok && secretArgRe.MatchString(strings.TrimLeft(k, "-")) {
			out = append(out, k+"=***")
			continue
		}
		if secretArgRe.MatchString(strings.TrimLeft(a, "-")) && i+1 < len(args) {
			out = append(out, a, "***")
			i++
			continue
		}
		out = append(out, a)
	}
	return out
}

// stringArgs 把 MCP 配置里读到的 args 转成字符串数组（可能来自 JSON 或 TOML）。
func stringArgs(v any) []string {
	switch list := v.(type) {
	case []string:
		return append([]string(nil), list...)
	case []any:
		out := make([]string, 0, len(list))
		for _, el := range list {
			out = append(out, fmt.Sprint(el))
		}
		return out
	}
	return nil
}

func (g *Gateway) importFoundProjectServer(name string) (library.MCPEntry, error) {
	for _, f := range discoverAllServers(g.libraryScanProjects(), g.library.Home(), map[string]bool{}) {
		if f.Name == name {
			raw, err := json.Marshal(f.Config)
			if err != nil {
				return library.MCPEntry{}, err
			}
			return g.library.SaveMCP(library.MCPEntry{Name: f.Name, Config: raw})
		}
	}
	return library.MCPEntry{}, os.ErrNotExist
}

func (g *Gateway) removeFoundProjectServers(name string) int {
	n := removeFoundServerDirs(g.libraryProjects(), name)
	return n + removeHomeServer(g.library.Home(), name)
}

func removeFoundServerDirs(projects []LibraryProject, name string) int {
	if name == "" {
		return 0
	}
	removed := 0
	for _, p := range projects {
		hit := false
		for _, f := range discoverProjectServers([]LibraryProject{p}, map[string]bool{}) {
			if f.Name == name {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		_ = applyProjectMCP(p.Dir, name, nil, nil)
		removed++
	}
	return removed
}

// handleFoundServerAction POST /api/library/servers/{action}
//
// 动作：import / remove-found / remove-found-some。
func (g *Gateway) handleFoundServerAction(w http.ResponseWriter, r *http.Request) {
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
			fail(http.StatusBadRequest, "缺少服务器名")
			return
		}
		if _, err := g.importFoundProjectServer(name); err != nil {
			fail(http.StatusNotFound, "项目里没有这个服务器: "+name)
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
			_, err := g.importFoundProjectServer(n)
			row := map[string]any{"name": n, "ok": err == nil}
			if err != nil {
				row["error"] = err.Error()
			} else {
				okN++
			}
			results = append(results, row)
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results, "ok": okN, "total": len(results)})
	case "remove-found":
		name := strings.TrimSpace(body.Name)
		if name == "" {
			fail(http.StatusBadRequest, "缺少服务器名")
			return
		}
		n := g.removeFoundProjectServers(name)
		if n == 0 {
			fail(http.StatusNotFound, "项目里没有这个服务器: "+name)
			return
		}
		g.writeLibraryView(w)
	case "remove-found-some":
		removed := map[string]int{}
		for _, n := range body.Names {
			n = strings.TrimSpace(n)
			if n != "" {
				removed[n] = g.removeFoundProjectServers(n)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
	default:
		fail(http.StatusNotFound, "未知动作: "+action)
	}
}

// ---- Agent 全局 MCP 配置（与 library.ApplyMCP 的目标一致） ----

func discoverHomeServers(home string, known map[string]bool) []foundProjectServer {
	if home == "" {
		return nil
	}
	var out []foundProjectServer
	add := func(agent, format, source string, servers map[string]any) {
		for n, v := range servers {
			if known[n] {
				continue
			}
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			cfg, ok := canonicalProjectServer(format, m)
			if !ok {
				continue
			}
			out = append(out, foundProjectServer{Name: n, Config: cfg, Agents: []string{agent}, Sources: []string{source}})
		}
	}
	addJSON := func(path, key, agent, format, source string) {
		if root, servers, err := readJSONServers(path, source, key); err == nil && root != nil {
			add(agent, format, source, servers)
		}
	}
	claudePath := filepath.Join(home, ".claude.json")
	if root, servers, err := readMCPJSON(claudePath, ".claude.json"); err == nil {
		add("claude_code", "claude", "Claude Code", servers)
		// Claude Code 把项目级 mcpServers 存在 ~/.claude.json 的 projects.<path>.mcpServers。
		if projects, ok := root["projects"].(map[string]any); ok {
			for _, pv := range projects {
				pm, _ := pv.(map[string]any)
				if pm == nil {
					continue
				}
				ms, _ := pm["mcpServers"].(map[string]any)
				if len(ms) > 0 {
					add("claude_code", "claude", "Claude Code · 项目", ms)
				}
			}
		}
	}
	if _, servers, err := readJSONServers(filepath.Join(home, ".gemini", "settings.json"), ".gemini/settings.json", "mcpServers"); err == nil {
		add("gemini_cli", "gemini", "Gemini CLI", servers)
	}
	if _, servers, err := readCodexFile(filepath.Join(home, ".codex", "config.toml")); err == nil {
		add("codex_cli", "codex", "Codex CLI", servers)
	}
	if _, servers, err := readOpenCodeServers(homeOpenCodeConfigPath(home), "opencode.json"); err == nil {
		add("opencode", "opencode", "opencode", servers)
	}
	if _, servers, err := readOpenCodeServers(filepath.Join(home, ".config", "mimocode", "mimocode.jsonc"), "mimocode.jsonc"); err == nil {
		add("mimocode", "opencode", "MiMo Code", servers)
	}
	// Qoder / WorkBuddy / CodeBuddy / ZCode 的 MCP 各自有独立文件。
	if raw, err := os.ReadFile(filepath.Join(home, ".qoder", "settings.json")); err == nil {
		var q struct {
			MCPServers map[string]any `json:"mcpServers"`
		}
		if json.Unmarshal(raw, &q) == nil && len(q.MCPServers) > 0 {
			add("qoder", "qoder", "Qoder", q.MCPServers)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(home, ".qoder-cn", "settings.json")); err == nil {
		var q struct {
			MCPServers map[string]any `json:"mcpServers"`
		}
		if json.Unmarshal(raw, &q) == nil && len(q.MCPServers) > 0 {
			add("qoder", "qoder", "Qoder CN", q.MCPServers)
		}
	}
	addJSON(filepath.Join(home, ".workbuddy", "mcp.json"), "mcpServers", "workbuddy", "claude", "WorkBuddy")
	addJSON(filepath.Join(home, ".workbuddy-ai", "mcp.json"), "mcpServers", "workbuddy", "claude", "WorkBuddy AI")
	addJSON(filepath.Join(home, ".codebuddy", "mcp.json"), "mcpServers", "codebuddy", "claude", "CodeBuddy")
	addJSON(filepath.Join(home, ".codebuddycn", "mcp.json"), "mcpServers", "codebuddy", "claude", "CodeBuddy CN")
	if raw, err := os.ReadFile(filepath.Join(home, ".zcode", "cli", "config.json")); err == nil {
		var z struct {
			MCP struct {
				Servers map[string]any `json:"servers"`
			} `json:"mcp"`
		}
		if json.Unmarshal(raw, &z) == nil && len(z.MCP.Servers) > 0 {
			add("zcode", "claude", "ZCode", z.MCP.Servers)
		}
	}
	if _, servers, err := readJSONServers(filepath.Join(home, ".cursor", "mcp.json"), ".cursor/mcp.json", "mcpServers"); err == nil {
		add("cursor", "claude", "Cursor", servers)
	}
	// 与 本机配置库 的 MCP 面板对齐：这些客户端也各自维护 mcpServers。
	addJSON(filepath.Join(home, ".gemini", "config", "mcp_config.json"), "mcpServers", "gemini_cli", "claude", "Gemini 配置")
	addJSON(filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), "mcpServers", "windsurf", "claude", "Windsurf")
	addJSON(filepath.Join(home, ".continue", "config.json"), "mcpServers", "continue", "claude", "Continue")
	addJSON(filepath.Join(home, ".kimi-code", "mcp.json"), "mcpServers", "kimi", "claude", "Kimi Code")
	addJSON(filepath.Join(home, ".minimax", "mcp.json"), "mcpServers", "mimocode", "claude", "MiMo Code")
	addJSON(filepath.Join(home, ".dsh", "mcp.json"), "mcpServers", "dsh", "claude", "DSH")
	addJSON(filepath.Join(home, ".pi", "agent", "mcp.json"), "mcpServers", "pi", "claude", "Pi")
	addJSON(filepath.Join(home, ".openclaw", "openclaw.json"), "mcpServers", "openclaw", "claude", "OpenClaw")
	if _, servers, err := readCodexFile(filepath.Join(home, ".grok", "config.toml")); err == nil {
		add("grok", "codex", "Grok Build", servers)
	}
	// Windows 客户端的配置在 %APPDATA%，不在用户主目录。
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		addJSON(filepath.Join(appData, "Claude", "claude_desktop_config.json"), "mcpServers", "claude_desktop", "claude", "Claude Desktop")
		addJSON(filepath.Join(appData, "Code", "User", "mcp.json"), "servers", "vscode", "claude", "VS Code")
		addJSON(filepath.Join(appData, "Code", "User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json"), "mcpServers", "cline", "claude", "Cline")
		addJSON(filepath.Join(appData, "Code", "User", "globalStorage", "rooveterinaryinc.roo-cline", "settings", "mcp_settings.json"), "mcpServers", "roo", "claude", "Roo Code")
		addJSON(filepath.Join(appData, "QoderCN", "SharedClientCache", "mcp.json"), "mcpServers", "qoder", "claude", "Qoder CN")
		matches, _ := filepath.Glob(filepath.Join(appData, "QoderCN", "SharedClientCache", "extension", "*", "mcp.json"))
		for _, p := range matches {
			addJSON(p, "mcpServers", "qoder", "claude", "Qoder CN")
		}
	}
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		// Claude 的 Store/MSIX 版本把配置放在 LocalCache 下，和 Roaming 版本并存。
		matches, _ := filepath.Glob(filepath.Join(local, "Packages", "Claude_*", "LocalCache", "Roaming", "Claude", "claude_desktop_config.json"))
		for _, p := range matches {
			addJSON(p, "mcpServers", "claude_desktop", "claude", "Claude Desktop")
		}
		matches, _ = filepath.Glob(filepath.Join(local, "CodeBuddyExtension", "Data", "*", "CodeBuddyIDE", "connectors", "mcp.json"))
		for _, p := range matches {
			addJSON(p, "mcpServers", "codebuddy", "claude", "CodeBuddy")
		}
	}
	// Gemini 插件自带的 MCP（如 science 插件）。
	for _, p := range geminiPluginRoots(home) {
		if root, servers, err := readJSONServers(filepath.Join(p.Dir, "mcp_config.json"), "mcp_config.json", "mcpServers"); err == nil && root != nil {
			add("gemini_cli", "gemini", p.Label, servers)
		}
	}
	return out
}

func discoverAllServers(projects []LibraryProject, home string, known map[string]bool) []foundProjectServer {
	all := discoverProjectServers(projects, known)
	all = append(all, discoverHomeServers(home, known)...)
	byName := map[string]*foundProjectServer{}
	for _, f := range all {
		if g, ok := byName[f.Name]; ok {
			for _, a := range f.Agents {
				if !hasAgent(g.Agents, a) {
					g.Agents = append(g.Agents, a)
				}
			}
			for _, src := range f.Sources {
				g.addSource(src)
			}
			continue
		}
		cp := f
		cp.Sources = append([]string(nil), f.Sources...)
		byName[f.Name] = &cp
	}
	out := make([]foundProjectServer, 0, len(byName))
	for _, f := range byName {
		sort.Strings(f.Agents)
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// backupCopy 在改写 Agent 全局配置前留一份 .aether.bak。
func backupCopy(path string) {
	if b, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".aether.bak", b, 0o600)
	}
}

// removeHomeServer 从四个全局 MCP 文件里删除同名条目，返回改动的文件数。
func removeHomeServer(home, name string) int {
	if home == "" || name == "" {
		return 0
	}
	n := 0
	claude := filepath.Join(home, ".claude.json")
	if root, servers, err := readMCPJSON(claude, ".claude.json"); err == nil && root != nil {
		if _, ok := servers[name]; ok {
			backupCopy(claude)
			delete(servers, name)
			if writeMCPFile(claude, root, servers) == nil {
				n++
			}
		}
	}
	gemini := filepath.Join(home, ".gemini", "settings.json")
	if root, servers, err := readJSONServers(gemini, ".gemini/settings.json", "mcpServers"); err == nil && root != nil {
		if _, ok := servers[name]; ok {
			backupCopy(gemini)
			delete(servers, name)
			if writeJSONServers(gemini, "mcpServers", root, servers) == nil {
				n++
			}
		}
	}
	codex := filepath.Join(home, ".codex", "config.toml")
	if text, servers, err := readCodexFile(codex); err == nil && servers[name] != nil {
		if out, removed := removeCodexSection(text, name); removed {
			backupCopy(codex)
			tmp := codex + ".aether.tmp"
			if os.WriteFile(tmp, []byte(out), 0o644) == nil && os.Rename(tmp, codex) == nil {
				n++
			}
		}
	}
	open := homeOpenCodeConfigPath(home)
	if root, servers, err := readOpenCodeServers(open, "opencode.json"); err == nil && root != nil {
		if _, ok := servers[name]; ok {
			backupCopy(open)
			delete(servers, name)
			if writeOpenCodeRoot(open, root) == nil {
				n++
			}
		}
	}
	return n
}
