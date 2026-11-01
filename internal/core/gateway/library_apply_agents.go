package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"aether-cockpit/internal/core/library"
	"aether-cockpit/internal/core/wsl"
)

// 资源库 Agent 芯片：插件把 servers/skills/hooks/subagents 的“给哪个 Agent 用”
// 统一成 POST /api/library/{kind}/agents[-some|-all] 与 /remove-all。
// 这里把 UI 的 Agent ID（claude_code…）折算成各 Store 的写入目标，并在写后
// 重新探测磁盘状态，确保返回的视图就是实际生效的结果。

var agentApplyTarget = map[string]string{
	"claude_code": "claude",
	"codex_cli":   "codex",
	"gemini_cli":  "gemini",
	"opencode":    "opencode",
}

var sharedSkillAgents = map[string]bool{
	"codex_cli": true, "gemini_cli": true, "opencode": true,
}

// libraryTargetFor 把库页面上的 agent id 折成（写入家目录, 基础 agent id）。
//
// 本机 agent（claude_code…）返回库自己的 home 与原名；WSL 客户端条目
// （wsl-ubuntu-22-04--claude_code）返回该发行版的家目录与折算后的基础 id，
// 这样同一套 Apply/Remove 逻辑不用改就能写进 WSL。
func (g *Gateway) libraryTargetFor(agent string) (home, base string, err error) {
	if mgr := g.clientsManager(); mgr != nil {
		if tgt, ok := mgr.TargetOf(agent); ok {
			if i := strings.Index(agent, "--"); i > 0 {
				base = agent[i+2:]
			} else {
				base = agent
			}
			if tgt.WSL != "" {
				// 家目录已经可访问（发行版在跑，或目标是可直达的 UNC）就不用再起
				// wsl.exe：这一步在生产里要 ~1s，在测试里则完全没必要。
				if !dirAccessible(tgt.Home) {
					if rerr := wsl.EnsureRunning(tgt.WSL); rerr != nil {
						return "", "", rerr
					}
				}
			}
			return tgt.Home, base, nil
		}
	}
	home = g.library.Home()
	if home == "" {
		return "", "", fmt.Errorf("未找到用户目录")
	}
	return home, agent, nil
}

// dirAccessible 报告路径是否存在且是目录。
func dirAccessible(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

type agentsChangeBody struct {
	Name   string   `json:"name"`
	Names  []string `json:"names"`
	Agents []string `json:"agents"`
	On     *bool    `json:"on"`
}

func (g *Gateway) agentSet(kind, name string) []string {
	switch kind {
	case "servers":
		return g.homeServerAgents()[name]
	case "skills":
		return g.appliedSkillAgents(name)
	case "hooks":
		if h := g.library.Hook(name); h != nil {
			return h.Agents
		}
	case "subagents":
		return g.appliedAgentAgents(name)
	}
	return nil
}

// appliedSkillAgents 汇总技能在本机与各 WSL 目标里的持有者（WSL agent 带前缀）。
func (g *Gateway) appliedSkillAgents(name string) []string {
	out := append([]string(nil), g.library.AppliedSkillAgents(name)...)
	for _, tgt := range g.wslTargets() {
		for _, a := range g.library.AppliedSkillAgentsTo(tgt.Home, name) {
			out = append(out, tgt.ID+"--"+a)
		}
	}
	return dedupeStrings(out)
}

// appliedAgentAgents 汇总子代理在本机与各 WSL 目标里的持有者。
func (g *Gateway) appliedAgentAgents(name string) []string {
	out := append([]string(nil), g.library.AppliedAgentAgents(name)...)
	for _, tgt := range g.wslTargets() {
		for _, a := range g.library.AppliedAgentAgentsTo(tgt.Home, name) {
			out = append(out, tgt.ID+"--"+a)
		}
	}
	return dedupeStrings(out)
}

func toSet(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, s := range list {
		m[s] = true
	}
	return m
}

func diffAgents(before, after []string) []string {
	b, a := toSet(before), toSet(after)
	out := []string{}
	for id := range a {
		if !b[id] {
			out = append(out, id)
		}
	}
	for id := range b {
		if !a[id] {
			out = append(out, id)
		}
	}
	return out
}

func (g *Gateway) applyAgentSet(kind, name string, want []string) ([]string, []map[string]any) {
	before := g.agentSet(kind, name)
	beforeSet := toSet(before)
	wantSet := toSet(want)
	problems := []map[string]any{}
	fail := func(agent string, err error) {
		problems = append(problems, map[string]any{"agent": agent, "error": err.Error()})
	}
	addedOK, removedOK := map[string]bool{}, map[string]bool{}
	for _, id := range want {
		if beforeSet[id] {
			continue
		}
		if err := g.addAgentTo(kind, name, id, wantSet); err != nil {
			fail(id, err)
		} else {
			addedOK[id] = true
		}
	}
	for _, id := range before {
		if wantSet[id] {
			continue
		}
		if err := g.removeAgentFrom(kind, name, id, wantSet); err != nil {
			fail(id, err)
		} else {
			removedOK[id] = true
		}
	}
	var after []string
	if kind == "hooks" {
		// hooks 的“已下发”记录在条目自身；只记录确实写成功的 Agent。
		next := []string{}
		for _, id := range before {
			if wantSet[id] || !removedOK[id] {
				next = append(next, id)
			}
		}
		for _, id := range want {
			if !beforeSet[id] && addedOK[id] {
				next = append(next, id)
			}
		}
		if h := g.library.Hook(name); h != nil {
			entry := *h
			entry.Agents = dedupeStrings(next)
			_, _ = g.library.SaveHook(entry)
		}
		after = dedupeStrings(next)
	} else {
		after = g.agentSet(kind, name)
	}
	return diffAgents(before, after), problems
}

func (g *Gateway) addAgentTo(kind, name, agent string, want map[string]bool) error {
	switch kind {
	case "servers":
		home, base, err := g.libraryTargetFor(agent)
		if err != nil {
			return err
		}
		target, ok := agentApplyTarget[base]
		if !ok {
			return fmt.Errorf("%s 不支持 MCP 服务器", agent)
		}
		snap := g.library.List()
		for _, m := range snap.MCPServers {
			if m.Name == name {
				_, err := g.library.ApplyMCPTo(home, m.ID, target)
				return err
			}
		}
		return fmt.Errorf("库中没有这个 MCP 服务器: %s", name)
	case "skills":
		home, base, err := g.libraryTargetFor(agent)
		if err != nil {
			return err
		}
		target := "agents"
		if base == "claude_code" {
			target = "claude"
		} else if !sharedSkillAgents[base] {
			return fmt.Errorf("%s 不支持技能下发", agent)
		}
		isWSL := base != agent
		snap := g.library.List()
		for _, sk := range snap.Skills {
			if sk.Name == name {
				// WSL 目标一律拷贝：9p 上的目录符号链接不可靠（客户端也未必跟随）。
				link := !isWSL && g.library.EffectiveSkillHow(target) == library.SkillHowLink
				_, err := g.library.ApplySkillAsTo(home, sk.ID, target, link)
				return err
			}
		}
		return fmt.Errorf("库中没有这个技能: %s", name)
	case "hooks":
		h := g.library.Hook(name)
		if h == nil {
			return fmt.Errorf("库中没有这个 hook: %s", name)
		}
		home, base, err := g.libraryTargetFor(agent)
		if err != nil {
			return err
		}
		return g.library.ApplyHookTo(home, *h, base)
	case "subagents":
		home, base, err := g.libraryTargetFor(agent)
		if err != nil {
			return err
		}
		if base != "claude_code" {
			return fmt.Errorf("%s 不支持子代理", agent)
		}
		snap := g.library.List()
		for _, a := range snap.Agents {
			if a.Name == name {
				_, err := g.library.ApplyAgentTo(home, a.ID, "claude")
				return err
			}
		}
		return fmt.Errorf("库中没有这个子代理: %s", name)
	}
	return fmt.Errorf("未知类型: %s", kind)
}

func (g *Gateway) removeAgentFrom(kind, name, agent string, want map[string]bool) error {
	switch kind {
	case "servers":
		home, base, err := g.libraryTargetFor(agent)
		if err != nil {
			return err
		}
		return removeHomeMCPServer(home, base, name)
	case "skills":
		home, base, err := g.libraryTargetFor(agent)
		if err != nil {
			return err
		}
		if base == "claude_code" {
			if want[agent] {
				return nil
			}
			return g.library.RemoveAppliedSkillTo(home, name, "claude")
		}
		if !sharedSkillAgents[base] {
			return fmt.Errorf("%s 不支持技能下发", agent)
		}
		// 共享目录（.agents/skills）仍被同一目标里的其他客户端使用时不删。
		prefix := ""
		if base != agent {
			if i := strings.Index(agent, "--"); i > 0 {
				prefix = agent[:i+2]
			}
		}
		for id := range sharedSkillAgents {
			if want[prefix+id] {
				return nil
			}
		}
		return g.library.RemoveAppliedSkillTo(home, name, "agents")
	case "hooks":
		h := g.library.Hook(name)
		if h == nil {
			return nil
		}
		home, base, err := g.libraryTargetFor(agent)
		if err != nil {
			return err
		}
		return g.library.RemoveHookTo(home, *h, base)
	case "subagents":
		home, base, err := g.libraryTargetFor(agent)
		if err != nil {
			return err
		}
		return g.library.RemoveAppliedAgentTo(home, name, base)
	}
	return fmt.Errorf("未知类型: %s", kind)
}

// removeHomeMCPServer 只从指定 Agent 的全局 MCP 配置里删除该服务器。
func removeHomeMCPServer(home, agent, name string) error {
	switch agent {
	case "claude_code", "gemini_cli":
		var path, label string
		if agent == "claude_code" {
			path, label = filepath.Join(home, ".claude.json"), ".claude.json"
		} else {
			path, label = filepath.Join(home, ".gemini", "settings.json"), ".gemini/settings.json"
		}
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		root, servers, err := readMCPJSON(path, label)
		if err != nil {
			return err
		}
		if _, ok := servers[name]; !ok {
			return nil
		}
		delete(servers, name)
		return writeMCPFile(path, root, servers)
	case "codex_cli":
		removeCodexMCP(filepath.Join(home, ".codex", "config.toml"), name)
		return nil
	case "opencode":
		path := homeOpenCodeConfigPath(home)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		root, servers, err := readOpenCodeServers(path, filepath.Base(path))
		if err != nil {
			return err
		}
		if _, ok := servers[name]; !ok {
			return nil
		}
		delete(servers, name)
		return writeOpenCodeRoot(path, root)
	}
	return fmt.Errorf("%s 不支持 MCP 服务器", agent)
}

func applySetAndView(g *Gateway, w http.ResponseWriter, kind, name string, want []string) {
	changed, problems := g.applyAgentSet(kind, name, want)
	out := map[string]any{"changed": changed}
	if len(problems) > 0 {
		out["problems"] = problems
	}
	g.writeLibraryViewExtra(w, applyResultExtra(out))
}

func applyResultExtra(res map[string]any) map[string]any {
	return map[string]any{"result": res}
}

// handleApplyAgents POST /api/library/{kind}/agents —— 一条条目的完整 Agent 集合。
func (g *Gateway) handleApplyAgents(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body agentsChangeBody
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		name := strings.TrimSpace(body.Name)
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少名称"})
			return
		}
		applySetAndView(g, w, kind, name, dedupeStrings(body.Agents))
	}
}

// handleApplyAgentsSome POST /api/library/{kind}/agents-some —— 多个条目同一批 Agent。
func (g *Gateway) handleApplyAgentsSome(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body agentsChangeBody
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		on := body.On == nil || *body.On
		changed := []string{}
		problems := []map[string]any{}
		for _, raw := range body.Names {
			name := strings.TrimSpace(raw)
			if name == "" {
				continue
			}
			cur := g.agentSet(kind, name)
			if on {
				cur = append(append([]string(nil), cur...), body.Agents...)
			} else {
				drop := toSet(body.Agents)
				kept := []string{}
				for _, id := range cur {
					if !drop[id] {
						kept = append(kept, id)
					}
				}
				cur = kept
			}
			c, p := g.applyAgentSet(kind, name, dedupeStrings(cur))
			changed = append(changed, c...)
			problems = append(problems, p...)
		}
		out := map[string]any{"changed": dedupeStrings(changed)}
		if len(problems) > 0 {
			out["problems"] = problems
		}
		g.writeLibraryViewExtra(w, applyResultExtra(out))
	}
}

// handleApplyAgentsAll POST /api/library/{kind}/agents-all —— 全部条目对一批 Agent 开/关。
func (g *Gateway) handleApplyAgentsAll(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body agentsChangeBody
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		on := body.On == nil || *body.On
		names := g.allNames(kind)
		changed := []string{}
		problems := []map[string]any{}
		for _, name := range names {
			cur := g.agentSet(kind, name)
			if on {
				cur = append(append([]string(nil), cur...), body.Agents...)
			} else {
				drop := toSet(body.Agents)
				kept := []string{}
				for _, id := range cur {
					if !drop[id] {
						kept = append(kept, id)
					}
				}
				cur = kept
			}
			c, p := g.applyAgentSet(kind, name, dedupeStrings(cur))
			changed = append(changed, c...)
			problems = append(problems, p...)
		}
		out := map[string]any{"changed": dedupeStrings(changed)}
		if len(problems) > 0 {
			out["problems"] = problems
		}
		g.writeLibraryViewExtra(w, applyResultExtra(out))
	}
}

func (g *Gateway) allNames(kind string) []string {
	snap := g.library.List()
	out := []string{}
	switch kind {
	case "servers":
		for _, m := range snap.MCPServers {
			out = append(out, m.Name)
		}
	case "skills":
		for _, s := range snap.Skills {
			out = append(out, s.Name)
		}
	case "hooks":
		for _, h := range g.library.HooksList() {
			out = append(out, h.Name)
		}
	case "subagents":
		for _, a := range snap.Agents {
			out = append(out, a.Name)
		}
	}
	return out
}

// handleRemoveAllKind POST /api/library/{kind}/remove-all —— 从库里删除条目。
// 已下发给 Agent 的副本保留（与插件文案“They stay in the library, to turn on again”
// 相反的方向：删除库条目不动 Agent 文件），失败项按 what 前缀回报。
func (g *Gateway) handleRemoveAllKind(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body agentsChangeBody
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		snap := g.library.List()
		changed := []string{}
		unremoved := []map[string]any{}
		prefix := map[string]string{"servers": "mcp:", "skills": "skill:", "hooks": "hook:", "subagents": "subagent:"}[kind]
		for _, raw := range body.Names {
			name := strings.TrimSpace(raw)
			if name == "" {
				continue
			}
			found := false
			switch kind {
			case "servers":
				for _, m := range snap.MCPServers {
					if m.Name == name {
						found = g.library.DeleteMCP(m.ID) || found
					}
				}
			case "skills":
				for _, s := range snap.Skills {
					if s.Name == name {
						found = g.library.DeleteSkill(s.ID) || found
					}
				}
			case "hooks":
				found = g.library.DeleteHook(name)
			case "subagents":
				for _, a := range snap.Agents {
					if a.Name == name {
						found = g.library.DeleteAgent(a.ID) || found
					}
				}
			}
			if found {
				changed = append(changed, name)
			} else {
				unremoved = append(unremoved, map[string]any{"what": prefix + name, "error": "条目不存在"})
			}
		}
		out := map[string]any{"changed": changed}
		if len(unremoved) > 0 {
			out["unremoved"] = unremoved
		}
		g.writeLibraryViewExtra(w, applyResultExtra(out))
	}
}
