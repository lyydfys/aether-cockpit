package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"

	"aether-cockpit/internal/core/library"
)

var hookNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// handleHooksSave POST /api/library/hooks/save
//
// 请求体：{"old": "旧名字", "hook": {name, description, event, matcher, command, timeout, agents}}。
// 先把旧 hook 从不再需要的 Agent 中移除，再写入新 hook，最后保存库条目。
func (g *Gateway) handleHooksSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Old  string            `json:"old"`
		Hook library.HookEntry `json:"hook"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	fail := func(status int, msg string) {
		writeJSON(w, status, map[string]any{"error": msg})
	}
	hook := body.Hook
	hook.Name = strings.TrimSpace(hook.Name)
	hook.Event = strings.TrimSpace(hook.Event)
	hook.Command = strings.TrimSpace(hook.Command)
	if !hookNamePattern.MatchString(hook.Name) {
		fail(http.StatusBadRequest, "hook 名字只能含字母、数字、- 和 _")
		return
	}
	if hook.Event == "" || hook.Command == "" {
		fail(http.StatusBadRequest, "缺少事件或命令")
		return
	}
	seen := map[string]bool{}
	agents := make([]string, 0, len(hook.Agents))
	for _, a := range hook.Agents {
		if seen[a] {
			continue
		}
		if !library.HookAgentSupported(a) {
			fail(http.StatusBadRequest, "暂不支持给 "+a+" 写 hooks")
			return
		}
		seen[a] = true
		agents = append(agents, a)
	}
	hook.Agents = agents

	oldName := strings.TrimSpace(body.Old)
	var old *library.HookEntry
	if oldName != "" {
		old = g.library.Hook(oldName)
	}

	var errs []string
	if old != nil {
		for _, a := range old.Agents {
			if !seen[a] {
				if err := g.library.RemoveHook(*old, a); err != nil {
					errs = append(errs, a+": "+err.Error())
				}
			}
		}
	}
	for _, a := range agents {
		if old != nil && hasAgent(old.Agents, a) {
			if err := g.library.RemoveHook(*old, a); err != nil {
				errs = append(errs, a+": "+err.Error())
				continue
			}
		}
		if err := g.library.ApplyHook(hook, a); err != nil {
			errs = append(errs, a+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		fail(http.StatusInternalServerError, "写入 hook 失败: "+strings.Join(errs, "；"))
		return
	}
	entry, err := g.library.SaveHook(hook)
	if err != nil {
		fail(http.StatusBadRequest, err.Error())
		return
	}
	if old != nil && old.Name != entry.Name {
		g.library.DeleteHook(old.Name)
	}
	g.writeLibraryView(w)
}

// handleHooksRemove POST /api/library/hooks/remove
//
// 请求体：{"name": "..."}：从所有 Agent 中移除并删掉库条目。
func (g *Gateway) handleHooksRemove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	name := strings.TrimSpace(body.Name)
	h := g.library.Hook(name)
	if h == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "库中没有这个 hook: " + name})
		return
	}
	for _, a := range h.Agents {
		_ = g.library.RemoveHook(*h, a)
	}
	g.library.DeleteHook(name)
	g.writeLibraryView(w)
}
