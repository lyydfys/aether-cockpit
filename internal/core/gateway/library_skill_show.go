package gateway

import (
	"net/http"
	"strings"
)

// GET /api/library/skill?name=<name>：资源库里的技能正文与磁盘路径，
// 供插件“看技能内容”的弹窗使用（找不到时明确 404）。
func (g *Gateway) handleLibrarySkillShow(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少技能名"})
		return
	}
	for _, s := range g.library.List().Skills {
		if s.Name != name {
			continue
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name": s.Name, "text": s.Content, "path": g.library.SkillFilePath(s.Name),
			"description": s.Description, "repo": s.Repo, "kind": s.Kind,
		})
		return
	}
	// 发现的（未入库）技能：用扫描时读到的内容。
	for _, f := range discoverAllSkills(g.libraryScanProjects(), g.library.Home(), map[string]bool{}) {
		if f.Name != name {
			continue
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"name": f.Name, "text": f.Content, "path": "", "description": f.Description,
		})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "库中没有这个技能: " + name})
}

// handleRTKUnsupported 对尚未实现的 RTK 动作给出原因，而不是 404。
func (g *Gateway) handleRTKUnsupported(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error": "RTK 集成尚未实现：需要在系统上安装并接管各 Agent 的 shell 钩子，当前版本不会自动执行第三方安装器。",
	})
}
