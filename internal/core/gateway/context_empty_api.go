package gateway

import (
	"io"
	"net/http"
	"strconv"
)

// 用量页的「上下文 / 为你调优」由 magpie 从网关保存的提示词正文计算。
// 本项目隐私优先、不落正文，因此如实返回空结果（页面已隐藏该标签），
// 避免前端轮询拿到 404 噪音。

func daysParam(r *http.Request) int {
	n := 7
	if v := r.URL.Query().Get("days"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 90 {
			n = parsed
		}
	}
	return n
}

// handleContextEmpty GET /api/context
func (g *Gateway) handleContextEmpty(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"days": daysParam(r), "agents": []any{}, "sessions": []any{},
	})
}

// handleTuneEmpty GET /api/tune
func (g *Gateway) handleTuneEmpty(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"days": daysParam(r), "agents": []any{}})
}

// handleTuneAction POST /api/tune/{path}：没有可调项，直接确认。
func (g *Gateway) handleTuneAction(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
