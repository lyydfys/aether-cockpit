package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"aether-cockpit/internal/core/sessions"
)

// 本文件提供会话页所需的数据端点。
//
// 数据来自各 Agent 自己写在磁盘上的会话文件（见 internal/core/sessions）。
// 这是只读的：网关只读不写，不会改动 Agent 的记录。

// handleSessions 返回会话列表，可按 agent / 关键词过滤。
//
//	GET /api/sessions?agent=claude-code&q=重构&limit=100
func (g *Gateway) handleSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	agent := q.Get("agent")
	kw := strings.ToLower(strings.TrimSpace(q.Get("q")))
	limit := 200
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}

	views := g.allSessionViews()
	out := make([]sessionUsageView, 0, len(views))
	agents := map[string]string{}
	for _, v := range views {
		if agent != "" && v.Agent != agent {
			continue
		}
		if kw != "" {
			hay := strings.ToLower(v.Title + " " + v.Cwd + " " + v.ID)
			if !strings.Contains(hay, kw) {
				continue
			}
		}
		agents[v.Agent] = v.Name
		out = append(out, v)
		if len(out) >= limit {
			break
		}
	}
	// aether 用量页读 {sessions, agents}（数组在这里会被当成空列表）。
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out, "agents": agents})
}

// handleSessionsStats 返回会话统计（用量页的「会话」标签读它）。
func (g *Gateway) handleSessionsStats(w http.ResponseWriter, r *http.Request) {
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 90 {
			days = n
		}
	}
	writeJSON(w, http.StatusOK, g.sessionsStatsView(days))
}

// handleSessionsOverview 返回按 Agent 分组的会话总览。
func (g *Gateway) handleSessionsOverview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days := 7
	if v := q.Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 90 {
			days = n
		}
	}
	writeJSON(w, http.StatusOK, g.sessionsOverviewView(days,
		strings.TrimSpace(q.Get("agent")), strings.TrimSpace(q.Get("model")), strings.TrimSpace(q.Get("cwd"))))
}

// handleSessionOne GET /api/sessions/one?key=agent:id
func (g *Gateway) handleSessionOne(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 key"})
		return
	}
	v, ok := g.sessionByKey(key)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "没有这个会话"})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleSessionsStub 覆盖会话页会请求但暂未实现语义的子端点。
//
// 返回空对象而不是 404：前端的 api() 在非 2xx 时会把响应体当成错误文案，
// Go 的 http.NotFound 正文正是 "404 page not found"，于是整页被这句话占满。
// 给一个空对象，页面按"没有数据"渲染，这才是正确行为。
func (g *Gateway) handleSessionsStub(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"session":    nil,
		"sessions":   []any{},
		"progress":   []any{},
		"transcript": []any{},
		"ok":         true,
	})
}

// sessionAgentName 是会话页上的 Agent 展示名。
func sessionAgentName(id string) string {
	switch id {
	case "claude-code":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "gemini-cli":
		return "Gemini CLI"
	}
	return id
}

// handleSessionsManage GET /api/sessions/manage?agent=
//
// 会话页（aether 的 Sessions 插件）读这个端点：一次给出 Agent 列表、
// 选中 Agent 的会话与回收站。deletable 恒为 false——网关只读 Agent 的
// 会话文件，不代删；resume 只给已知命令形态的 Claude Code。
func (g *Gateway) handleSessionsManage(w http.ResponseWriter, r *http.Request) {
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	all := sessions.Scan()

	byAgent := make(map[string][]sessions.Session, 4)
	order := make([]string, 0, 4)
	for _, s := range all {
		if _, ok := byAgent[s.Agent]; !ok {
			order = append(order, s.Agent)
		}
		byAgent[s.Agent] = append(byAgent[s.Agent], s)
	}
	if agent == "" && len(order) > 0 {
		agent = order[0]
	}

	agents := make([]map[string]any, 0, len(order))
	for _, id := range order {
		list := byAgent[id]
		var last int64
		if len(list) > 0 {
			last = list[0].Last * 1000
		}
		agents = append(agents, map[string]any{
			"id": id, "name": sessionAgentName(id), "icon": id,
			"count": len(list), "sessions": len(list),
			"deletable": false, "last": last,
		})
	}

	list := make([]map[string]any, 0, len(byAgent[agent]))
	for _, s := range byAgent[agent] {
		// resume 覆盖扫描器支持的全部三个客户端（原来只给 Claude Code 拼）。
		resume := resumeCommand(s.Agent, s.ID)
		list = append(list, map[string]any{
			"id": s.ID, "agent": s.Agent,
			"title": s.Title, "name": s.Title,
			"cwd": s.Cwd, "path": s.Path, "wsl": s.WSL,
			"last": s.Last * 1000, "size": s.Size,
			"models": []any{}, "messages": 0, "start": 0,
			"deletable": false, "gateway": false,
			"transcript": true, "resume": resume,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agent":     agent,
		"agents":    agents,
		"sessions":  list,
		"trash":     []any{},
		"deletable": false,
		"recording": false,
		"recorded":  0,
	})
}

// handleSessionsTranscript GET /api/sessions/transcript?agent=&id=
//
// 只读 Agent 自己的会话文件；找不到文件时返回空 parts（页面显示
// "这里没有说过话"），而不是 404。
func (g *Gateway) handleSessionsTranscript(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	id := r.URL.Query().Get("id")
	parts, cut, ok := sessions.Transcript(agent, id)
	if parts == nil {
		parts = []sessions.TranscriptPart{}
	}
	source := "file"
	if !ok {
		source = "gateway"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"parts":  parts,
		"cut":    cut,
		"source": source,
	})
}

// handleSessionsSearch GET /api/sessions/search?agent=&q=
func (g *Gateway) handleSessionsSearch(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	q := r.URL.Query().Get("q")
	hits, searched, total, cut := sessions.Search(agent, q)
	if hits == nil {
		hits = []sessions.Hit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hits": hits, "searched": searched, "total": total,
		"cut": cut, "unread": false,
	})
}

func sessionMarkdownName(agent, id string) string {
	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	if short == "" {
		short = "session"
	}
	return agent + "-" + short + ".md"
}

// handleSessionsMarkdown GET /api/sessions/markdown?agent=&id=
//
// 浏览器版直接下载；桌面版走 /api/sessions/export 落盘。
func (g *Gateway) handleSessionsMarkdown(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	id := r.URL.Query().Get("id")
	md, ok := sessions.Markdown(agent, id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "找不到该会话文件"})
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+sessionMarkdownName(agent, id)+`"`)
	_, _ = w.Write([]byte(md))
}

// handleSessionsExport POST /api/sessions/export?agent=&id=
func (g *Gateway) handleSessionsExport(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	id := r.URL.Query().Get("id")
	md, ok := sessions.Markdown(agent, id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "找不到该会话文件"})
		return
	}
	dir := ""
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, "Downloads")
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			dir = candidate
		}
	}
	if dir == "" {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, sessionMarkdownName(agent, id))
	if err := os.WriteFile(path, []byte(md), 0o600); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "写入失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path})
}
