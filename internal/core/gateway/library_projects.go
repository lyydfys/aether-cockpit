package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"aether-cockpit/internal/core/jsoncutil"
	"aether-cockpit/internal/core/library"

	toml "github.com/pelletier/go-toml/v2"
)

// 资源库 projects：把库里的 skills / MCP servers 放进用户显式添加的项目目录。
//
// 安全边界：只写项目目录内的 `.claude/skills`、`.agents/skills`、
// `.mcp.json`、`.codex/config.toml` 与 `.gemini/settings.json` 中本项目
// 放置的条目；撤销只删这些条目，不触碰其他文件。
// 复制模式（aether 的 link/copy 选择简化为复制）。

type LibraryProject struct {
	Dir     string              `json:"dir"`
	Name    string              `json:"name"`
	Copy    bool                `json:"copy"`
	Skills  map[string][]string `json:"skills,omitempty"`
	Servers map[string][]string `json:"servers,omitempty"`
}

var projectNameStrip = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safeProjectSkillName(name string) string {
	n := projectNameStrip.ReplaceAllString(strings.TrimSpace(name), "-")
	n = strings.Trim(n, "-.")
	if n == "" {
		n = "skill"
	}
	return n
}

func expandHomePath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func (g *Gateway) libraryProjects() []LibraryProject {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	out := make([]LibraryProject, len(g.settings.LibraryProjects))
	copy(out, g.settings.LibraryProjects)
	return out
}

// libraryScanProjects 是只读发现用的项目集合：用户显式添加的项目 +
// 从 ~/.claude.json 自动识别的本机项目。自动项目只参与扫描，不写设置、
// 不参与“写入项目”动作，避免把未确认的目录当管理目标。
func (g *Gateway) libraryScanProjects() []LibraryProject {
	explicit := g.libraryProjects()
	out := make([]LibraryProject, 0, len(explicit)+16)
	seen := map[string]bool{}
	add := func(p LibraryProject) {
		dir := strings.TrimSpace(p.Dir)
		if dir == "" {
			return
		}
		key := strings.ToLower(filepath.Clean(dir))
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, p)
	}
	for _, p := range explicit {
		add(p)
	}
	for _, p := range discoverClaudeProjects(g.library.Home()) {
		add(p)
	}
	return out
}

// discoverClaudeProjects 读取 Claude Code 的 projects 键，作为只读发现的项目根。
// 过滤系统目录、用户主目录本身与不存在的路径，避免扫描 Windows/Temp。
func discoverClaudeProjects(home string) []LibraryProject {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		Projects map[string]json.RawMessage `json:"projects"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Projects) == 0 {
		return nil
	}
	out := make([]LibraryProject, 0, len(doc.Projects))
	for key := range doc.Projects {
		dir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(key)))
		if !usableProjectDir(home, dir) {
			continue
		}
		out = append(out, LibraryProject{Dir: dir, Name: filepath.Base(dir)})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Dir) < strings.ToLower(out[j].Dir) })
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func usableProjectDir(home, dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil || strings.TrimSpace(abs) == "" {
		return false
	}
	abs = filepath.Clean(abs)
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return false
	}
	homeAbs, _ := filepath.Abs(home)
	if strings.EqualFold(abs, filepath.Clean(homeAbs)) {
		return false
	}
	if filepath.VolumeName(abs)+string(filepath.Separator) == abs {
		return false
	}
	lower := strings.ToLower(abs)
	skip := []string{`c:\$recycle.bin`}
	for _, env := range []string{"WINDIR", "ProgramFiles", "ProgramFiles(x86)", "ProgramData", "TEMP", "TMP"} {
		if p := strings.TrimSpace(os.Getenv(env)); p != "" {
			skip = append(skip, strings.ToLower(filepath.Clean(p)))
		}
	}
	for _, p := range skip {
		if p == "" {
			continue
		}
		if lower == p || strings.HasPrefix(lower, p+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

func (g *Gateway) libraryProjectsView() []map[string]any {
	list := g.libraryProjects()
	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		skills := map[string][]string{}
		for k, v := range p.Skills {
			skills[k] = append([]string(nil), v...)
		}
		servers := map[string][]string{}
		for k, v := range p.Servers {
			servers[k] = append([]string(nil), v...)
		}
		_, err := os.Stat(p.Dir)
		out = append(out, map[string]any{
			"dir": p.Dir, "name": p.Name, "copy": p.Copy,
			"skills": skills, "servers": servers,
			"missing": err != nil, "problems": map[string]any{},
		})
	}
	return out
}

func (g *Gateway) saveLibraryProjects(list []LibraryProject) error {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	g.settings.LibraryProjects = list
	return g.saveSettings()
}

func (g *Gateway) respondLibrary(w http.ResponseWriter, r *http.Request) {
	rec := &discardResponse{header: http.Header{}}
	g.handleLibrary(rec, r)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	w.WriteHeader(rec.status)
	_, _ = w.Write(rec.body)
}

func hasAgent(agents []string, id string) bool {
	for _, a := range agents {
		if a == id {
			return true
		}
	}
	return false
}

// ensureIgnoreLine 幂等地把一行写进项目 .gitignore。
func ensureIgnoreLine(dir, line string) error {
	path := filepath.Join(dir, ".gitignore")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(raw)
	for _, l := range strings.Split(content, "\n") {
		if strings.TrimSpace(l) == line {
			return nil
		}
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += line + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func removeIgnoreLine(dir, line string) {
	path := filepath.Join(dir, ".gitignore")
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(raw), "\n")
	out := make([]string, 0, len(lines))
	removed := false
	for _, l := range lines {
		if strings.TrimSpace(l) == line {
			removed = true
			continue
		}
		out = append(out, l)
	}
	if !removed {
		return
	}
	_ = os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644)
}

// projectSkillAgents 是各 Agent 在项目里读技能的目录（取自 aether 的
// projectSkillsDirs）：Claude Code 用 .claude/skills，其余读共享 .agents/skills。
var projectSkillAgents = map[string]string{
	"claude_code": ".claude/skills",
	"codex_cli":   ".agents/skills",
	"gemini_cli":  ".agents/skills",
	"opencode":    ".agents/skills",
}

// applyProjectSkills 按 agents 维护每个目标目录里的技能副本与 .gitignore。
func applyProjectSkills(dir, name, content string, agents []string) error {
	return applyProjectSkillsMode(dir, name, content, agents, false, "")
}

// applyProjectSkillsMode 按 link 选择复制或链接。链接模式先由库写出
// canonical 目录，这里只建目录链接；撤销时只删链接本身，避免删到库内文件。
func applyProjectSkillsMode(dir, name, content string, agents []string, link bool, canonicalDir string) error {
	safe := safeProjectSkillName(name)
	want := map[string]bool{}
	for _, a := range agents {
		if rel, ok := projectSkillAgents[a]; ok {
			want[rel] = true
		}
	}
	if link && canonicalDir == "" {
		return fmt.Errorf("链接模式缺少 canonical 目录")
	}
	for _, rel := range []string{".claude/skills", ".agents/skills"} {
		target := filepath.Join(dir, filepath.FromSlash(rel), safe)
		line := "/" + rel + "/" + safe + "/"
		if want[rel] {
			if link {
				if err := replaceWithDirLink(target, canonicalDir); err != nil {
					return err
				}
			} else {
				if library.IsDirLink(target) {
					if err := os.Remove(target); err != nil {
						return err
					}
				}
				if err := os.MkdirAll(target, 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte(content), 0o644); err != nil {
					return err
				}
			}
			if err := ensureIgnoreLine(dir, line); err != nil {
				return err
			}
		} else {
			if library.IsDirLink(target) {
				_ = os.Remove(target)
			} else {
				_ = os.Remove(filepath.Join(target, "SKILL.md"))
				_ = os.Remove(target)
			}
			removeIgnoreLine(dir, line)
		}
	}
	return nil
}

// replaceWithDirLink 把 target 换成指向 canonicalDir 的目录链接。已有真实
// 目录只允许仅含 SKILL.md（我们自己复制的副本），含其他文件时拒绝。
func replaceWithDirLink(target, canonicalDir string) error {
	if library.IsDirLink(target) {
		if err := os.Remove(target); err != nil {
			return err
		}
	} else if entries, err := os.ReadDir(target); err == nil {
		if len(entries) == 0 {
			_ = os.Remove(target)
		} else if len(entries) == 1 && entries[0].Name() == "SKILL.md" {
			if err := os.RemoveAll(target); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("目标已有同名技能目录且含其他文件，未改为链接: %s", target)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return library.DirLink(canonicalDir, target)
}

// placeProjectSkill / removeProjectSkill 是 Claude Code 单目标的便捷封装（测试用）。
func placeProjectSkill(dir, name, content string) error {
	return applyProjectSkills(dir, name, content, []string{"claude_code"})
}

func removeProjectSkill(dir, name string) {
	_ = applyProjectSkills(dir, name, "", nil)
}

func readMCPServers(path string) (map[string]any, map[string]any, error) {
	return readMCPJSON(path, ".mcp.json")
}

func readMCPJSON(path, label string) (map[string]any, map[string]any, error) {
	return readJSONServers(path, label, "mcpServers")
}

func readJSONServers(path, label, key string) (map[string]any, map[string]any, error) {
	root := map[string]any{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return root, map[string]any{}, nil
	}
	if strings.TrimSpace(string(raw)) == "" {
		return root, map[string]any{}, nil
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, nil, fmt.Errorf("%s 不是合法 JSON，未改动: %w", label, err)
	}
	servers, _ := root[key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	return root, servers, nil
}

func writeMCPFile(path string, root map[string]any, servers map[string]any) error {
	return writeJSONServers(path, "mcpServers", root, servers)
}

func writeJSONServers(path, key string, root map[string]any, servers map[string]any) error {
	root[key] = servers
	return writeJSONFile(path, root)
}

// writeOpenCodeRoot 写 OpenCode 配置：.jsonc 只替换 mcp 成员，注释保留。
func writeOpenCodeRoot(path string, root map[string]any) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	if strings.HasSuffix(strings.ToLower(path), ".jsonc") || !json.Valid(raw) {
		out, err := jsoncutil.SpliceMember(raw, "mcp", root["mcp"])
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		tmp := path + ".aether.tmp"
		if err := os.WriteFile(tmp, out, 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}
	return writeJSONFile(path, root)
}

// openCodeLayout 取 opencode.json 的 mcp 容器：OpenCode 2 把服务器放在
// mcp.servers（且 servers 没有 type/command/url 自己的键），V1 直接放
// mcp.<name>；已有原生的 servers 对象时沿用原生布局。
func openCodeLayout(root map[string]any) (map[string]any, error) {
	if v, exists := root["mcp"]; exists {
		if _, ok := v.(map[string]any); !ok {
			return nil, fmt.Errorf("现有 opencode.json 里的 mcp 不是 JSON 对象，拒绝改写")
		}
	}
	mcp, ok := root["mcp"].(map[string]any)
	if !ok {
		mcp = map[string]any{}
		root["mcp"] = mcp
	}
	if servers, ok := mcp["servers"].(map[string]any); ok {
		own := false
		for _, k := range []string{"type", "command", "url"} {
			if _, has := servers[k]; has {
				own = true
				break
			}
		}
		if !own {
			return servers, nil
		}
	}
	return mcp, nil
}

func readOpenCodeServers(path, label string) (map[string]any, map[string]any, error) {
	root := map[string]any{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return root, map[string]any{}, nil
	}
	if strings.TrimSpace(string(raw)) == "" {
		return root, map[string]any{}, nil
	}
	normalized, err := jsoncutil.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s 不是合法 JSONC，未改动: %w", label, err)
	}
	if err := json.Unmarshal(normalized, &root); err != nil {
		return nil, nil, fmt.Errorf("%s 不是合法 JSON，未改动: %w", label, err)
	}
	servers, err := openCodeLayout(root)
	if err != nil {
		return nil, nil, err
	}
	return root, servers, nil
}

func writeJSONFile(path string, root map[string]any) error {
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".aether.tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// projectMCPAgents 是各 Agent 在项目里读 MCP 服务器的文件名与格式，
// 与 aether 的 projectMCPFiles 一致：Claude Code 读 .mcp.json，Codex 读
// 受信项目里的 .codex/config.toml，Gemini CLI 读 .gemini/settings.json。
var projectMCPAgents = map[string]struct {
	Rel    string
	Format string
}{
	"claude_code": {".mcp.json", "claude"},
	"codex_cli":   {".codex/config.toml", "codex"},
	"gemini_cli":  {".gemini/settings.json", "gemini"},
	"opencode":    {"opencode.json", "opencode"},
}

func projectMCPOrder() []string {
	return []string{"claude_code", "codex_cli", "gemini_cli", "opencode"}
}

// openCodeConfigPath 返回项目里的 OpenCode 配置：优先 opencode.json，
// 项目已有 opencode.jsonc 时用它（不改写另一份）。
func openCodeConfigPath(dir string) string {
	jsonPath := filepath.Join(dir, "opencode.json")
	if _, err := os.Stat(jsonPath); err == nil {
		return jsonPath
	}
	jsoncPath := filepath.Join(dir, "opencode.jsonc")
	if _, err := os.Stat(jsoncPath); err == nil {
		return jsoncPath
	}
	return jsonPath
}

func homeOpenCodeConfigPath(home string) string {
	base := filepath.Join(home, ".config", "opencode")
	jsonPath := filepath.Join(base, "opencode.json")
	if _, err := os.Stat(jsonPath); err == nil {
		return jsonPath
	}
	jsoncPath := filepath.Join(base, "opencode.jsonc")
	if _, err := os.Stat(jsoncPath); err == nil {
		return jsoncPath
	}
	return jsonPath
}

// mcpEntryFor 把库里的 Claude 形状配置折成目标 Agent 的文件形状：
// Gemini 用 httpUrl（streamable HTTP）/ url（SSE）区分传输，Codex 只有
// stdio 与 streamable HTTP，SSE 直接拒绝而不是写成错的形状。
func mcpEntryFor(format string, obj any) (map[string]any, error) {
	m, ok := obj.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("MCP 配置必须是 JSON 对象")
	}
	if format == "claude" {
		return m, nil
	}
	if format == "opencode" {
		out := map[string]any{}
		rawURL, _ := m["url"].(string)
		cmd, _ := m["command"].(string)
		if strings.TrimSpace(rawURL) != "" {
			out["type"] = "remote"
			out["url"] = rawURL
			out["enabled"] = true
			if h, ok := m["headers"].(map[string]any); ok && len(h) > 0 {
				out["headers"] = h
			}
			return out, nil
		}
		if strings.TrimSpace(cmd) == "" {
			return nil, fmt.Errorf("MCP 配置缺少 command 或 url")
		}
		command := []any{cmd}
		if args, ok := m["args"].([]any); ok {
			command = append(command, args...)
		}
		out["type"] = "local"
		out["command"] = command
		out["enabled"] = true
		if env, ok := m["env"].(map[string]any); ok && len(env) > 0 {
			out["environment"] = env
		}
		return out, nil
	}
	out := map[string]any{}
	rawURL, _ := m["url"].(string)
	typ, _ := m["type"].(string)
	cmd, _ := m["command"].(string)
	switch {
	case strings.TrimSpace(rawURL) != "":
		if format == "codex" {
			if typ == "sse" {
				return nil, fmt.Errorf("Codex 的项目配置不支持 SSE")
			}
			out["url"] = rawURL
			if h, ok := m["headers"].(map[string]any); ok && len(h) > 0 {
				out["http_headers"] = h
			}
			return out, nil
		}
		if typ == "sse" {
			out["url"] = rawURL
		} else {
			out["httpUrl"] = rawURL
		}
		if h, ok := m["headers"].(map[string]any); ok && len(h) > 0 {
			out["headers"] = h
		}
	case strings.TrimSpace(cmd) != "":
		out["command"] = cmd
		if args, ok := m["args"].([]any); ok && len(args) > 0 {
			out["args"] = args
		}
		if env, ok := m["env"].(map[string]any); ok && len(env) > 0 {
			out["env"] = env
		}
	default:
		return nil, fmt.Errorf("MCP 配置缺少 command 或 url")
	}
	return out, nil
}

// applyProjectMCP 按 agents 维护三个 Agent 的项目 MCP 文件；不再勾选的
// Agent 只删本项目写过的同名条目，文件其余内容原样保留。
func applyProjectMCP(dir, name string, cfg json.RawMessage, agents []string) error {
	var obj any
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &obj); err != nil {
			return fmt.Errorf("MCP 配置不是合法 JSON: %w", err)
		}
	}
	var errs []string
	for _, agentID := range projectMCPOrder() {
		t := projectMCPAgents[agentID]
		if !hasAgent(agents, agentID) {
			removeProjectMCPEntry(dir, name, agentID)
			continue
		}
		entry, err := mcpEntryFor(t.Format, obj)
		if err != nil {
			errs = append(errs, t.Rel+": "+err.Error())
			continue
		}
		if err := writeProjectMCPEntry(dir, name, t.Rel, t.Format, entry); err != nil {
			errs = append(errs, "写入 "+t.Rel+" 失败: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "；"))
	}
	return nil
}

func writeProjectMCPEntry(dir, name, rel, format string, entry map[string]any) error {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if format == "codex" {
		return writeCodexMCP(path, name, entry)
	}
	if format == "opencode" {
		path := openCodeConfigPath(dir)
		root, servers, err := readOpenCodeServers(path, filepath.Base(path))
		if err != nil {
			return err
		}
		servers[name] = entry
		return writeOpenCodeRoot(path, root)
	}
	_, statErr := os.Stat(path)
	root, servers, err := readJSONServers(path, rel, "mcpServers")
	if err != nil {
		return err
	}
	servers[name] = entry
	if err := writeJSONServers(path, "mcpServers", root, servers); err != nil {
		return err
	}
	if format == "claude" && os.IsNotExist(statErr) {
		// 文件是我们新建的：整个忽略；原来就有的只加键，不动 .gitignore。
		return ensureIgnoreLine(dir, "/.mcp.json")
	}
	return nil
}

func removeProjectMCPEntry(dir, name, agentID string) {
	t, ok := projectMCPAgents[agentID]
	if !ok {
		return
	}
	path := filepath.Join(dir, filepath.FromSlash(t.Rel))
	if t.Format == "codex" {
		removeCodexMCP(path, name)
		return
	}
	if t.Format == "opencode" {
		path := openCodeConfigPath(dir)
		root, servers, err := readOpenCodeServers(path, filepath.Base(path))
		if err != nil {
			return
		}
		if _, ok := servers[name]; !ok {
			return
		}
		delete(servers, name)
		_ = writeOpenCodeRoot(path, root)
		return
	}
	root, servers, err := readJSONServers(path, t.Rel, "mcpServers")
	if err != nil {
		return
	}
	if _, ok := servers[name]; !ok {
		return
	}
	delete(servers, name)
	_ = writeJSONServers(path, "mcpServers", root, servers)
}

// placeProjectServer / removeProjectServer 是 Claude Code 单目标的便捷封装。
func placeProjectServer(dir, name string, cfg json.RawMessage) error {
	return applyProjectMCP(dir, name, cfg, []string{"claude_code"})
}

func removeProjectServer(dir, name string) {
	removeProjectMCPEntry(dir, name, "claude_code")
}

// readCodexFile 读取 .codex/config.toml；不是合法 TOML 时拒绝改动，
// 避免用户的其他配置被覆盖。
func readCodexFile(path string) (string, map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", map[string]any{}, nil
	}
	text := string(raw)
	if strings.TrimSpace(text) == "" {
		return text, map[string]any{}, nil
	}
	var doc map[string]any
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return "", nil, fmt.Errorf("%s 不是合法 TOML，未改动: %w", filepath.Base(path), err)
	}
	servers, _ := doc["mcp_servers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	return text, servers, nil
}

func tomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func tomlInline(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(t))
		for _, k := range keys {
			parts = append(parts, tomlString(k)+" = "+tomlInline(t[k]))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	case []any:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			parts = append(parts, tomlInline(x))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case string:
		return tomlString(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return `""`
	default:
		return tomlString(fmt.Sprint(t))
	}
}

func codexSectionHead(trimmed, name string) bool {
	for _, h := range []string{"[mcp_servers." + tomlString(name) + "]", "[mcp_servers." + name + "]"} {
		if trimmed == h {
			return true
		}
		// 子表，如 [mcp_servers.name.env]
		if strings.HasPrefix(trimmed, h[:len(h)-1]+".") && strings.HasSuffix(trimmed, "]") {
			return true
		}
	}
	return false
}

// removeCodexSection 删掉某个服务器的整段（含子表），其余逐行保留。
func removeCodexSection(text, name string) (string, bool) {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	skip, removed := false, false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if skip && strings.HasPrefix(t, "[") && !codexSectionHead(t, name) {
			skip = false
		}
		if codexSectionHead(t, name) {
			skip, removed = true, true
			continue
		}
		if skip {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), removed
}

func writeCodexMCP(path, name string, entry map[string]any) error {
	text, _, err := readCodexFile(path)
	if err != nil {
		return err
	}
	text, _ = removeCodexSection(text, name)
	text = strings.TrimRight(text, "\n")
	var b strings.Builder
	if text != "" {
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	b.WriteString("[mcp_servers." + tomlString(name) + "]\n")
	seen := map[string]bool{}
	for _, k := range []string{"url", "http_headers", "command", "args", "env"} {
		v, ok := entry[k]
		if !ok || v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		if m, ok := v.(map[string]any); ok && len(m) == 0 {
			continue
		}
		if a, ok := v.([]any); ok && len(a) == 0 {
			continue
		}
		b.WriteString(k + " = " + tomlInline(v) + "\n")
		seen[k] = true
	}
	rest := make([]string, 0, len(entry))
	for k := range entry {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		b.WriteString(k + " = " + tomlInline(entry[k]) + "\n")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".aether.tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func removeCodexMCP(path, name string) {
	text, _, err := readCodexFile(path)
	if err != nil {
		return
	}
	out, removed := removeCodexSection(text, name)
	if !removed {
		return
	}
	tmp := path + ".aether.tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o644); err == nil {
		_ = os.Rename(tmp, path)
	}
}

// handleLibraryProjectAction POST /api/library/projects/{action}
func (g *Gateway) handleLibraryProjectAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var body struct {
		Dir    string   `json:"dir"`
		Name   string   `json:"name"`
		Agents []string `json:"agents"`
		Copy   bool     `json:"copy"`
		Keep   bool     `json:"keep"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	fail := func(status int, msg string) {
		writeJSON(w, status, map[string]any{"error": msg})
	}
	dir := filepath.Clean(expandHomePath(body.Dir))
	list := g.libraryProjects()
	find := func() int {
		for i := range list {
			if list[i].Dir == dir {
				return i
			}
		}
		return -1
	}

	switch action {
	case "add":
		if dir == "" || dir == "." {
			fail(http.StatusBadRequest, "请填写项目目录")
			return
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			fail(http.StatusBadRequest, "项目目录不存在: "+dir)
			return
		}
		if find() >= 0 {
			g.respondLibrary(w, r)
			return
		}
		list = append(list, LibraryProject{
			Dir: dir, Name: filepath.Base(dir), Copy: true,
			Skills: map[string][]string{}, Servers: map[string][]string{},
		})
	case "remove":
		idx := find()
		if idx < 0 {
			fail(http.StatusNotFound, "没有这个项目")
			return
		}
		if !body.Keep {
			for name, agents := range list[idx].Skills {
				if len(agents) > 0 {
					removeProjectSkill(dir, name)
				}
			}
			for name, agents := range list[idx].Servers {
				if len(agents) > 0 {
					_ = applyProjectMCP(dir, name, nil, nil)
				}
			}
		}
		list = append(list[:idx], list[idx+1:]...)
	case "copy":
		idx := find()
		if idx < 0 {
			fail(http.StatusNotFound, "没有这个项目")
			return
		}
		if list[idx].Copy != body.Copy {
			list[idx].Copy = body.Copy
			// 已跟踪的技能按新模式重新下发，现有内容立即切换。
			for name, agents := range list[idx].Skills {
				if len(agents) == 0 {
					continue
				}
				content := ""
				for _, sk := range g.library.List().Skills {
					if sk.Name == name {
						content = sk.Content
						break
					}
				}
				if content == "" {
					continue
				}
				canonical := ""
				if !list[idx].Copy {
					c, err := g.library.MaterializeSkill(name, content)
					if err != nil {
						fail(http.StatusInternalServerError, "准备技能目录失败: "+err.Error())
						return
					}
					canonical = c
				}
				if err := applyProjectSkillsMode(dir, name, content, agents, !list[idx].Copy, canonical); err != nil {
					fail(http.StatusInternalServerError, "切换技能方式失败: "+err.Error())
					return
				}
			}
		}
	case "skill":
		idx := find()
		if idx < 0 {
			fail(http.StatusNotFound, "没有这个项目")
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			fail(http.StatusBadRequest, "缺少技能名")
			return
		}
		wanted := false
		for _, a := range body.Agents {
			if _, ok := projectSkillAgents[a]; ok {
				wanted = true
				break
			}
		}
		content := ""
		if wanted {
			snap := g.library.List()
			found := false
			for _, sk := range snap.Skills {
				if sk.Name == name {
					content, found = sk.Content, true
					break
				}
			}
			if !found {
				fail(http.StatusNotFound, "库中没有这个技能: "+name)
				return
			}
		}
		link := !list[idx].Copy
		canonical := ""
		if wanted && link {
			c, err := g.library.MaterializeSkill(name, content)
			if err != nil {
				fail(http.StatusInternalServerError, "准备技能目录失败: "+err.Error())
				return
			}
			canonical = c
		}
		if err := applyProjectSkillsMode(dir, name, content, body.Agents, link, canonical); err != nil {
			fail(http.StatusInternalServerError, "写入技能失败: "+err.Error())
			return
		}
		if list[idx].Skills == nil {
			list[idx].Skills = map[string][]string{}
		}
		if len(body.Agents) == 0 {
			delete(list[idx].Skills, name)
		} else {
			list[idx].Skills[name] = body.Agents
		}
	case "server":
		idx := find()
		if idx < 0 {
			fail(http.StatusNotFound, "没有这个项目")
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			fail(http.StatusBadRequest, "缺少 MCP 服务器名")
			return
		}
		wanted := false
		for _, a := range body.Agents {
			if _, ok := projectMCPAgents[a]; ok {
				wanted = true
				break
			}
		}
		var cfg json.RawMessage
		if wanted {
			snap := g.library.List()
			found := false
			for _, m := range snap.MCPServers {
				if m.Name == name {
					cfg, found = m.Config, true
					break
				}
			}
			if !found {
				fail(http.StatusNotFound, "库中没有这个 MCP 服务器: "+name)
				return
			}
		}
		if err := applyProjectMCP(dir, name, cfg, body.Agents); err != nil {
			fail(http.StatusInternalServerError, "写入项目 MCP 配置失败: "+err.Error())
			return
		}
		if list[idx].Servers == nil {
			list[idx].Servers = map[string][]string{}
		}
		if len(body.Agents) == 0 {
			delete(list[idx].Servers, name)
		} else {
			list[idx].Servers[name] = body.Agents
		}
	default:
		fail(http.StatusNotFound, "未知动作: "+action)
		return
	}

	if err := g.saveLibraryProjects(list); err != nil {
		fail(http.StatusInternalServerError, "保存项目失败: "+err.Error())
		return
	}
	g.respondLibrary(w, r)
}
