package gateway

import "net/http"

// 会话写入（删除/清空/恢复/迁移）在本项目是只读设计：隐私优先，
// 不在网关里改写用户的会话记录。这里明确 501，而不是返回空对象 200
// 让人以为已经成功；会话页按 deletable=false 隐藏这些操作。
func (g *Gateway) handleSessionsWriteReadOnly(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error": "会话在 Aether Cockpit 中为只读设计：请在对应的 Agent 里管理会话记录。",
	})
}
