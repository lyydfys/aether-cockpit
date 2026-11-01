package gateway

import (
	"net/http"
)

// 本文件补齐 magpie 前端在页面初始化阶段会调用的辅助端点。
//
// 这些端点在 magpie 里属于「桌面壳能力」（CLI 版本发现、安装引导、
// 更新检查、状态页聚合）。我们不做同名能力时也必须返回结构正确的空值：
// 前端的 api() 会把 404 的正文当错误文案，一个未注册端点就能让整页
// 显示 "404 page not found"。

// handleUpstream GET /api/upstream
func (g *Gateway) handleUpstream(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"vendors":   []any{},
		"providers": map[string]string{},
	})
}

// handleWhatsNew GET /api/whatsnew
func (g *Gateway) handleWhatsNew(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
}

// handleWhatsNewSeen POST /api/whatsnew/seen
func (g *Gateway) handleWhatsNewSeen(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleWhatsNewToday POST /api/whatsnew/today
func (g *Gateway) handleWhatsNewToday(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"on": false})
}

// handlePluginUpdates GET /api/plugins/updates
func (g *Gateway) handlePluginUpdates(w http.ResponseWriter, r *http.Request) {
	// 前端读 u.waiting.length；对象形态，缺省即"没有待更新插件"。
	writeJSON(w, http.StatusOK, map[string]any{"waiting": []any{}})
}
