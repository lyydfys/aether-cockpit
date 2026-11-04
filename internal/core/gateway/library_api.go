package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"aether-cockpit/internal/core/library"
)

// 库 API —— prompts / MCP 服务器 / skills / subagents 的统一管理与一键写入。
//
// apply 端点会真实改写用户的客户端配置文件（记忆文件 / MCP 配置 / 技能目录 /
// 子代理定义文件），因此 store 层在每次写入前自动备份；这里只负责参数校验与结果聚合。

// handleLibrary GET /api/library —— 全量列出三类条目。
func (g *Gateway) handleLibrary(w http.ResponseWriter, r *http.Request) {
	snap := g.library.List()

	// 同时输出两套命名：
	//   prompts / mcp_servers / skills / agents 是内核原生的；
	//   skills / mcp / hooks / subagents / all 是前端（aether 改造版）读的。
	// 前端把条目按 kind 分桶渲染，字段是 name/title/kind/agents/on。
	row := func(id, name, kind string, at int64) map[string]any {
		title := name
		if title == "" {
			title = id
		}
		return map[string]any{
			"id": id, "name": name, "title": title,
			"kind": kind, "agents": []string{}, "on": true,
			"updated_at": at,
		}
	}

	skills := make([]map[string]any, 0, len(snap.Prompts)+len(snap.Skills))
	for _, p := range snap.Prompts {
		skills = append(skills, row(p.ID, p.Name, "skill", p.CreatedAt))
	}
	for _, sk := range snap.Skills {
		skills = append(skills, row(sk.ID, sk.Name, "skill", sk.CreatedAt))
	}

	mcp := make([]map[string]any, 0, len(snap.MCPServers))
	for _, m := range snap.MCPServers {
		mcp = append(mcp, row(m.ID, m.Name, "mcp", m.CreatedAt))
	}

	subagents := make([]map[string]any, 0, len(snap.Agents))
	for _, a := range snap.Agents {
		subagents = append(subagents, row(a.ID, a.Name, "subagent", a.CreatedAt))
	}

	all := make([]map[string]any, 0, len(skills)+len(mcp)+len(subagents))
	all = append(all, skills...)
	all = append(all, mcp...)
	all = append(all, subagents...)

	out := map[string]any{
		"prompts":     snap.Prompts,
		"mcp_servers": redactMCPEntries(snap.MCPServers),
		"skills_raw":  snap.Skills,
		"agents_raw":  snap.Agents,

		"skills":    skills,
		"mcp":       mcp,
		"hooks":     []any{},
		"subagents": subagents,
		"all":       all,
	}
	// aether 的资源库插件读的是另一套字段（agents/servers/instructions/
	// skills/projects/home）。两套字段同时下发，插件页与内核 API 都能工作。
	for k, v := range g.libraryPluginView(snap) {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

// redactMCPEntries 返回一份"结构可见、凭据不可见"的副本。
//
// 两层掩码：
//  1. OAuth 的 clientSecret 直接清空（沿用原有行为）；
//  2. config 里的敏感值（env / headers 的值、args 里 `--flag=value` 的值）换成掩码。
//
// 第 2 条是必须的：MCP 的 `env` 通常就是 API_KEY，原先它原样出现在
// `GET /api/library` 的响应里——任何拿到管理会话的客户端（含手机端）都能读到明文。
// 掩码只作用于**外发视图**，`library.json` 里仍是真值，下发给 Agent 时不受影响。
func redactMCPEntries(in []library.MCPEntry) []library.MCPEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]library.MCPEntry, len(in))
	for i, m := range in {
		cp := m
		if m.OAuth != nil {
			o := *m.OAuth
			o.ClientSecret = ""
			cp.OAuth = &o
		}
		cp.Config = maskMCPConfig(m.Config)
		out[i] = cp
	}
	return out
}

// maskMCPConfig 把 config 里的凭据值换成掩码，保留键名与命令，便于页面显示结构。
//
// 规则：
//   - `env` / `headers`：保留键，值走 maskHeaderValue（`••••` + 末 4 位）；
//   - `args`：只对 `--flag=value` 形式掩值，保留 flag 本身；
//   - 其余字段（command、url、transport…）原样保留；
//   - **解不开或不是对象 → 整体置空**。宁可少给，也不把未知结构原样外发。
//
// 注意：重新序列化会让 JSON 的键顺序变化。目前仓库里没有任何代码依赖这个顺序。
func maskMCPConfig(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil
	}
	for _, key := range []string{"env", "headers"} {
		sub, ok := obj[key].(map[string]any)
		if !ok {
			continue
		}
		masked := make(map[string]any, len(sub))
		for k, v := range sub {
			if s, ok := v.(string); ok {
				masked[k] = maskHeaderValue(s)
				continue
			}
			// 非字符串值（数字/布尔）也可能是凭据的一部分：统一掩掉，保留键。
			masked[k] = "••••"
		}
		obj[key] = masked
	}
	if args, ok := obj["args"].([]any); ok {
		next := make([]any, 0, len(args))
		for _, a := range args {
			s, isStr := a.(string)
			if !isStr {
				next = append(next, a)
				continue
			}
			next = append(next, maskMCPArg(s))
		}
		obj["args"] = next
	}
	buf, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return buf
}

// maskMCPArg 只处理 `--flag=value` / `-f=value` 这种内联赋值形式：保留 flag，掩掉值。
// 没有 `=` 的参数（例如 `mcp-server-fetch`）原样保留——它不是凭据。
func maskMCPArg(arg string) string {
	eq := strings.IndexByte(arg, '=')
	if eq <= 0 {
		return arg
	}
	flag := arg[:eq]
	if !strings.HasPrefix(flag, "-") {
		return arg
	}
	return flag + "=" + maskHeaderValue(arg[eq+1:])
}

// maskMCPArgs 批量版本。注意：`/api/library` 有**两条**路径会把 args 带出去——
// `mcp_servers[].config.args` 和插件视图 `servers[].args`，两处都必须掩。
func maskMCPArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = maskMCPArg(a)
	}
	return out
}

// libraryPluginView 把内核库映射成 aether 资源库页面的模型。
//
// 映射关系（概念不同，但不丢信息）：
//
//	prompts    → instructions.sets（共享指令集，第一条为启用项）
//	MCP 服务器 → servers
//	skills     → skills
//	子代理      → subagents
//	客户端      → agents（可写入的目标）
func (g *Gateway) libraryPluginView(snap library.Snapshot) map[string]any {
	agents := make([]map[string]any, 0, 8)
	instrAgents := make([]map[string]any, 0, 8)
	for _, st := range g.clientsManager().List() {
		entry := map[string]any{
			"id": st.ID, "name": st.Name, "icon": st.ID,
			"instructions": true, "mcp": true, "skills": true,
			"gone": false,
		}
		if rel, ok := projectSkillAgents[st.ID]; ok {
			entry["projectSkills"] = rel
		}
		if st.ID == "claude_code" || st.ID == "codex_cli" || st.ID == "gemini_cli" || st.ID == "opencode" {
			how, own := g.library.AgentSkillHow(st.ID)
			target := "agents"
			if st.ID == "claude_code" {
				target = "claude"
			}
			if !own {
				how = g.library.EffectiveSkillHow(target)
			}
			entry["how"] = how
			entry["howOwn"] = own
			entry["mustCopy"] = false
		}
		if t, ok := projectMCPAgents[st.ID]; ok {
			entry["projectMCP"] = t.Rel
		}
		agents = append(agents, entry)
		// 指令行会显示 name/icon/path；这里同时读出文件现状，页面不用再猜
		// “有路径”是不是“已经有内容”。内容只读，导入动作另走 prompts 保存。
		// 没有用户级指令文件约定的客户端（含 WSL 目标）不进这张表：
		// 否则会出现大量空路径行，页面看起来仍像“全是空白”。
		path := instructionsPath(g.library.Home(), st.ID)
		entry["instructions"] = path != ""
		if path == "" {
			continue
		}
		text, size, modified, exists, truncated := readLocalText(path)
		instrAgents = append(instrAgents, map[string]any{
			"agent": st.ID, "name": st.Name, "icon": st.ID,
			"path": path, "file": filepath.Base(path),
			"on": true, "extra": "", "instructions": true,
			"exists": exists, "size": size, "modified": modified,
			"content": text, "truncated": truncated,
		})
	}

	hooksView := make([]map[string]any, 0, 8)
	for _, h := range g.library.HooksList() {
		hooksView = append(hooksView, map[string]any{
			"name": h.Name, "description": h.Description, "event": h.Event,
			"matcher": h.Matcher, "command": h.Command, "timeout": h.Timeout, "agents": h.Agents,
		})
	}

	sets := make([]map[string]any, 0, len(snap.Prompts))
	for i, p := range snap.Prompts {
		sets = append(sets, map[string]any{
			"id": p.ID, "name": p.Name, "text": p.Content, "active": i == 0,
			"file": "", "agents": []string{},
		})
	}

	homeServerAgents := g.homeServerAgents()

	servers := make([]map[string]any, 0, len(snap.MCPServers))
	for _, m := range snap.MCPServers {
		var cfg struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			URL     string   `json:"url"`
			Type    string   `json:"type"`
		}
		_ = json.Unmarshal(m.Config, &cfg)
		transport := "stdio"
		if cfg.URL != "" || cfg.Type == "http" || cfg.Type == "sse" {
			transport = "http"
		}
		agents := homeServerAgents[m.Name]
		if agents == nil {
			agents = []string{}
		}
		servers = append(servers, map[string]any{
			"name": m.Name, "transport": transport,
			"command": cfg.Command, "args": maskMCPArgs(cfg.Args), "url": cfg.URL,
			"agents": agents, "icon": "generic",
			"signIn": g.mcpSignInView(m.Name, cfg.URL),
			"disabledTools": func() []string {
				if m.DisabledTools == nil {
					return []string{}
				}
				return m.DisabledTools
			}(),
			"oauth": func() map[string]any {
				if m.OAuth == nil || m.OAuth.ClientID == "" {
					return nil
				}
				// 只回显 clientId 与固定回调端口；clientSecret 永不外发。
				out := map[string]any{"clientId": m.OAuth.ClientID}
				if m.OAuth.CallbackPort > 0 {
					out["callbackPort"] = m.OAuth.CallbackPort
				}
				return out
			}(),
		})
	}

	skillsPlugin := make([]map[string]any, 0, len(snap.Skills))
	for _, s := range snap.Skills {
		kind := s.Kind
		if kind == "" {
			kind = "local"
		}
		appliedAgents := g.appliedSkillAgents(s.Name)
		if appliedAgents == nil {
			appliedAgents = []string{}
		}
		row := map[string]any{
			"name": s.Name, "kind": kind, "agents": appliedAgents, "always": []string{},
			"source": "", "edited": s.InstalledContent != "" && s.InstalledContent != s.Content,
			"description": s.Description, "mine": s.Group,
		}
		if s.Repo != "" {
			row["repo"] = s.Repo
			row["source"] = s.Repo
		}
		if s.SkillPath != "" {
			row["path"] = s.SkillPath
		}
		if c := g.skillCheckOf(s.Name); c != nil {
			row["check"] = c
		}
		skillsPlugin = append(skillsPlugin, row)
	}

	subagentsPlugin := make([]map[string]any, 0, len(snap.Agents))
	for _, a := range snap.Agents {
		agents := g.appliedAgentAgents(a.Name)
		if agents == nil {
			agents = []string{}
		}
		subagentsPlugin = append(subagentsPlugin, map[string]any{
			"name": a.Name, "description": a.Description,
			"tools": a.Tools, "model": a.Model, "agents": agents,
			"prompt": a.Content, "skills": a.Skills, "mcp": a.MCP,
		})
	}

	return map[string]any{
		"home":         g.library.Home(),
		"agents":       agents,
		"servers":      servers,
		"skills":       skillsPlugin,
		"hooks":        hooksView,
		"subagents":    subagentsPlugin,
		"foundServers": g.foundServersView(), "foundSkills": g.foundSkillsView(),
		"foundSubagents": g.foundSubagentsView(),
		"newSkills":      []any{}, "partial": false, "checkDue": false,
		"copySkills": g.library.SkillHowSetting() != library.SkillHowLink,
		"backups":    g.library.BackupDir(),
		"hookAgents": []map[string]any{
			{"id": "claude_code", "events": library.HookAgentEvents("claude_code")},
			{"id": "codex_cli", "events": library.HookAgentEvents("codex_cli")},
			{"id": "gemini_cli", "events": library.HookAgentEvents("gemini_cli")},
		},
		"projects":     g.libraryProjectsView(),
		"problems":     []any{},
		"instructions": map[string]any{"sets": sets, "agents": instrAgents, "files": g.localInstructionDocs()},
	}
}

// handleSavePrompt POST /api/library/prompts
//
// 请求体：{"name": "...", "description": "...", "content": "..."}
// name 与 content 必填。
func (g *Gateway) handleSavePrompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Content     string `json:"content"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name 不能为空"})
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "content 不能为空"})
		return
	}
	entry, err := g.library.SavePrompt(library.PromptEntry{
		Name:        body.Name,
		Description: body.Description,
		Content:     body.Content,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": entry})
}

// handleDeletePrompt DELETE /api/library/prompts/{id}
func (g *Gateway) handleDeletePrompt(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !g.library.DeletePrompt(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "条目不存在: " + id})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleApplyPrompt POST /api/library/prompts/{id}/apply
//
// 请求体：{"targets": ["claude", "codex", "gemini"]}
// 单个目标失败不影响其它目标，逐目标返回结果。
func (g *Gateway) handleApplyPrompt(w http.ResponseWriter, r *http.Request) {
	targets, err := decodeApplyTargets(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	id := r.PathValue("id")
	results, okN := runApply(func(target string) (string, error) {
		return g.library.ApplyPrompt(id, target)
	}, targets)
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "ok": okN, "total": len(results)})
}

// handleSaveMCP POST /api/library/mcp
//
// 请求体：{"name": "...", "config": {...一个 MCP server 的完整定义对象...}}
func (g *Gateway) handleSaveMCP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name 不能为空"})
		return
	}
	// 顶层必须是 JSON 对象：JSON 合并与 TOML 渲染都按对象解释。
	var probe map[string]any
	if err := json.Unmarshal(body.Config, &probe); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "config 必须是 JSON 对象"})
		return
	}
	entry, err := g.library.SaveMCP(library.MCPEntry{Name: body.Name, Config: body.Config})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": entry})
}

// handleDeleteMCP DELETE /api/library/mcp/{id}
func (g *Gateway) handleDeleteMCP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !g.library.DeleteMCP(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "条目不存在: " + id})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleApplyMCP POST /api/library/mcp/{id}/apply
//
// 请求体：{"targets": ["claude", "codex", "gemini"]}
func (g *Gateway) handleApplyMCP(w http.ResponseWriter, r *http.Request) {
	targets, err := decodeApplyTargets(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	id := r.PathValue("id")
	results, okN := runApply(func(target string) (string, error) {
		return g.library.ApplyMCP(id, target)
	}, targets)
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "ok": okN, "total": len(results)})
}

// handleSaveSkill POST /api/library/skills
//
// 请求体：{"name": "...", "description": "...", "content": "SKILL.md 全文"}
func (g *Gateway) handleSaveSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Content     string `json:"content"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name 不能为空"})
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "content 不能为空"})
		return
	}
	entry, err := g.library.SaveSkill(library.SkillEntry{
		Name:        body.Name,
		Description: body.Description,
		Content:     body.Content,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": entry})
}

// handleDeleteSkill DELETE /api/library/skills/{id}
func (g *Gateway) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !g.library.DeleteSkill(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "条目不存在: " + id})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleApplySkill POST /api/library/skills/{id}/apply
//
// 请求体：{"targets": ["claude", "agents"]}
func (g *Gateway) handleApplySkill(w http.ResponseWriter, r *http.Request) {
	targets, err := decodeApplyTargets(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	id := r.PathValue("id")
	results, okN := runApply(func(target string) (string, error) {
		return g.library.ApplySkillAs(id, target, g.library.EffectiveSkillHow(target) == library.SkillHowLink)
	}, targets)
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "ok": okN, "total": len(results)})
}

// handleSkillHow POST /api/library/skills/how
//
// 请求体：{"agent": "" | "<agent id>", "how": "copy" | "link"}；空 agent 为库默认。
func (g *Gateway) handleSkillHow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent string `json:"agent"`
		How   string `json:"how"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	agent := strings.TrimSpace(body.Agent)
	if err := g.library.SetSkillHow(agent, body.How); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	g.writeLibraryView(w)
}

// handleSaveAgent POST /api/library/agents
//
// 请求体：{"name": "...", "description": "...", "tools": "Read, Grep", "model": "sonnet", "content": "系统提示词"}
// name 与 content 必填；tools / model 是 frontmatter 可选字段的原样字符串，留空则 apply 时不写该行。
func (g *Gateway) handleSaveAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Tools       string `json:"tools"`
		Model       string `json:"model"`
		Content     string `json:"content"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name 不能为空"})
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "content 不能为空"})
		return
	}
	entry, err := g.library.SaveAgent(library.AgentEntry{
		Name:        body.Name,
		Description: body.Description,
		Tools:       body.Tools,
		Model:       body.Model,
		Content:     body.Content,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": entry})
}

// handleDeleteAgent DELETE /api/library/agents/{id}
func (g *Gateway) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !g.library.DeleteAgent(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "条目不存在: " + id})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleApplyAgent POST /api/library/agents/{id}/apply
//
// 请求体：{"targets": ["claude"]}——Claude Code 是唯一有子代理文件约定的客户端。
func (g *Gateway) handleApplyAgent(w http.ResponseWriter, r *http.Request) {
	targets, err := decodeApplyTargets(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	id := r.PathValue("id")
	results, okN := runApply(func(target string) (string, error) {
		return g.library.ApplyAgent(id, target)
	}, targets)
	writeJSON(w, http.StatusOK, map[string]any{"results": results, "ok": okN, "total": len(results)})
}

// decodeApplyTargets 解析 apply 请求的 targets 数组。
func decodeApplyTargets(r *http.Request) ([]string, error) {
	var body struct {
		Targets []string `json:"targets"`
	}
	if err := decodeBody(r, &body); err != nil {
		return nil, err
	}
	if len(body.Targets) == 0 {
		return nil, errTargetsRequired
	}
	return body.Targets, nil
}

var errTargetsRequired = errors.New("缺少 targets")

// runApply 依次对每个目标执行 apply，聚合结果：单目标失败不影响其它目标。
// 返回结果数组与成功数。
func runApply(apply func(target string) (string, error), targets []string) ([]library.ApplyResult, int) {
	results := make([]library.ApplyResult, 0, len(targets))
	okN := 0
	for _, t := range targets {
		res := library.ApplyResult{Target: t}
		if path, err := apply(t); err != nil {
			res.Error = err.Error()
		} else {
			res.OK = true
			res.Path = path
			okN++
		}
		results = append(results, res)
	}
	return results, okN
}

// handleLibraryStub 覆盖资料库页其余端点，返回空对象而不是 404。
func (g *Gateway) handleLibraryStub(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"items": []any{}, "servers": []any{}, "about": map[string]any{},
		"ok": true,
	})
}

// handleLibraryInstructionsSave POST /api/library/instructions/save
//
// aether 资源库页的共享指令集动作（新建 / 改名 / 改正文 / 删除 / 开关 Agent）。
// 内核库里对应的条目是 prompts：id/name/content 一一映射；
// 启停 Agent 与文件改写属于 aether 的写文件模型，这里只接受并返回当前视图。
func (g *Gateway) handleLibraryInstructionsSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Texts  map[string]string `json:"texts"`
		Rename *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"rename"`
		Remove string `json:"remove"`
		Create *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"create"`
		Activate string          `json:"activate"`
		Agents   map[string]bool `json:"agents"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	snap := g.library.List()
	find := func(id string) *library.PromptEntry {
		for i := range snap.Prompts {
			if snap.Prompts[i].ID == id {
				return &snap.Prompts[i]
			}
		}
		return nil
	}

	if body.Create != nil && strings.TrimSpace(body.Create.Name) != "" {
		if _, err := g.library.SavePrompt(library.PromptEntry{
			ID: body.Create.ID, Name: body.Create.Name,
			Content: "# " + body.Create.Name + "\n",
		}); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	if body.Rename != nil {
		if p := find(body.Rename.ID); p != nil && strings.TrimSpace(body.Rename.Name) != "" {
			p.Name = body.Rename.Name
			if _, err := g.library.SavePrompt(*p); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
		}
	}
	if len(body.Texts) > 0 {
		for id, text := range body.Texts {
			if p := find(id); p != nil {
				p.Content = text
				if _, err := g.library.SavePrompt(*p); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
					return
				}
			}
		}
	}
	if body.Remove != "" {
		g.library.DeletePrompt(body.Remove)
	}

	out := g.libraryPluginView(g.library.List())
	out["part"] = "instructions"
	out["problems"] = []any{}
	out["result"] = map[string]any{"ok": true}
	writeJSON(w, http.StatusOK, out)
}

// handleLibraryRTK GET /api/library/rtk
//
// RTK（终端输出压缩）是 aether 的桌面能力，本项目没有随包安装的 rtk；
// 返回不可用状态，页面会显示说明而不是转圈或报错。
func (g *Gateway) handleLibraryRTK(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"available": false, "install": false, "pathDir": false, "agents": []any{},
	})
}

// instructionsPath 返回各客户端读取的用户级指令文件位置（用于资源库展示）。
// 同时接受内核 client ID（claude_code）与插件页的短名（claude-code）。
func instructionsPath(home, clientID string) string {
	if home == "" {
		return ""
	}
	switch clientID {
	case "claude_code", "claude-code":
		return filepath.Join(home, ".claude", "CLAUDE.md")
	case "codex_cli", "codex":
		return filepath.Join(home, ".codex", "AGENTS.md")
	case "gemini_cli", "gemini-cli":
		return filepath.Join(home, ".gemini", "GEMINI.md")
	case "opencode":
		return filepath.Join(home, ".config", "opencode", "AGENTS.md")
	case "mimocode":
		return filepath.Join(home, ".config", "mimocode", "AGENTS.md")
	}
	return ""
}

// writeLibraryView 返回资源库插件认识的全量视图（变更接口的返回值）。
func (g *Gateway) writeLibraryView(w http.ResponseWriter) {
	g.writeLibraryViewExtra(w, nil)
}

func (g *Gateway) writeLibraryViewExtra(w http.ResponseWriter, extra map[string]any) {
	out := g.libraryPluginView(g.library.List())
	out["problems"] = []any{}
	for k, v := range extra {
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLibraryServerSave POST /api/library/servers/save
func (g *Gateway) handleLibraryServerSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Old    string `json:"old"`
		Server struct {
			Name       string            `json:"name"`
			Transport  string            `json:"transport"`
			Agents     []string          `json:"agents"`
			Command    string            `json:"command"`
			Args       []string          `json:"args"`
			Env        map[string]string `json:"env"`
			URL        string            `json:"url"`
			Headers    map[string]string `json:"headers"`
			StartupSec int               `json:"startupTimeout"`
			OAuth      *struct {
				ClientID     string `json:"clientId"`
				ClientSecret string `json:"clientSecret"`
				CallbackPort int    `json:"callbackPort"`
			} `json:"oauth"`
		} `json:"server"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	name := strings.TrimSpace(body.Server.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "服务器名不能为空"})
		return
	}
	// 地址/命令为空时存下去的条目永远检查不出东西（/mcp/check 会直接跳过
	// 它），界面上表现为"保存成功但什么都没发生"。宁可 400 说清楚。
	if strings.TrimSpace(body.Server.Transport) == "stdio" {
		if strings.TrimSpace(body.Server.Command) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "stdio 服务器需要 command"})
			return
		}
	} else if strings.TrimSpace(body.Server.URL) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "http/sse 服务器需要 url"})
		return
	}
	cfg := map[string]any{}
	if body.Server.Transport == "stdio" {
		cfg["command"] = body.Server.Command
		if len(body.Server.Args) > 0 {
			cfg["args"] = body.Server.Args
		}
		if len(body.Server.Env) > 0 {
			cfg["env"] = body.Server.Env
		}
	} else {
		// SSE 是独立传输（GET 事件流 + POST 消息），存成 http 会让检查与
		// 下发都走错协议；以前这里无条件写 "http"。
		cfg["type"] = "http"
		if strings.TrimSpace(body.Server.Transport) == "sse" {
			cfg["type"] = "sse"
		}
		cfg["url"] = body.Server.URL
		if len(body.Server.Headers) > 0 {
			cfg["headers"] = body.Server.Headers
		}
	}
	raw, _ := json.Marshal(cfg)
	entry := library.MCPEntry{Name: name, Config: raw}
	var prevOAuth *library.MCPOAuthClient
	for _, m := range g.library.List().MCPServers {
		if (body.Old != "" && m.Name == body.Old) || m.Name == name {
			entry.ID = m.ID
			entry.CreatedAt = m.CreatedAt
			entry.DisabledTools = m.DisabledTools
			prevOAuth = m.OAuth
			break
		}
	}
	if body.Server.OAuth != nil && strings.TrimSpace(body.Server.OAuth.ClientID) != "" {
		port := body.Server.OAuth.CallbackPort
		if port < 0 || port > 65535 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "回调端口必须在 1–65535 之间"})
			return
		}
		sec := strings.TrimSpace(body.Server.OAuth.ClientSecret)
		if sec == "" && prevOAuth != nil && prevOAuth.ClientID == body.Server.OAuth.ClientID {
			sec = prevOAuth.ClientSecret // 留空沿用旧 secret
		}
		entry.OAuth = &library.MCPOAuthClient{
			ClientID: strings.TrimSpace(body.Server.OAuth.ClientID), ClientSecret: sec, CallbackPort: port,
		}
	}
	if _, err := g.library.SaveMCP(entry); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	g.writeLibraryView(w)
}

// handleLibraryRemoveByName 处理按名字删除的 servers/skills/subagents remove。
func (g *Gateway) handleLibraryRemoveByName(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
			return
		}
		name := strings.TrimSpace(body.Name)
		snap := g.library.List()
		switch kind {
		case "servers":
			for _, m := range snap.MCPServers {
				if m.Name == name {
					g.library.DeleteMCP(m.ID)
				}
			}
		case "skills":
			for _, s := range snap.Skills {
				if s.Name == name {
					g.library.DeleteSkill(s.ID)
				}
			}
		case "subagents":
			for _, a := range snap.Agents {
				if a.Name == name {
					for _, id := range g.library.AppliedAgentAgents(a.Name) {
						_ = g.library.RemoveAppliedAgent(a.Name, id)
					}
					g.library.DeleteAgent(a.ID)
				}
			}
		}
		g.writeLibraryView(w)
	}
}
