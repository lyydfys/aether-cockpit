package gateway

import (
	"net/http"
	"time"

	"aether-cockpit/internal/core/state"
)

// 响应快照库（state.db）的容量管理接口。
//
// 对应用户实际遇到的问题：长期使用后 state.db 涨到 650 MB，界面上完全
// 看不到它的存在，也没有任何清理入口。根因是 state.pruneSQLite 定义了
// 却没有任何生产调用方——过期行永不删除，库只增不减。

// handleStateStoreStats GET /api/system/state
func (g *Gateway) handleStateStoreStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.store.Stats())
}

// handleStateStoreCompact POST /api/system/state/compact
//
// 支持 {"keep": N, "vacuum": bool}；都不传时用库自身的上限并在确有删除时回收。
func (g *Gateway) handleStateStoreCompact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keep   int   `json:"keep"`
		Vacuum *bool `json:"vacuum"`
	}
	_ = decodeBody(r, &body)

	vacuum := true
	if body.Vacuum != nil {
		vacuum = *body.Vacuum
	}
	res, err := g.store.Compact(state.CompactOptions{
		KeepRecords: body.Keep,
		Vacuum:      vacuum,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "清理失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result": res,
		"stats":  g.store.Stats(),
	})
}

// startStateStoreAutoCompact 让清理自动发生，不必用户手动点。
func (g *Gateway) startStateStoreAutoCompact() func() {
	// 软上限 128 MB：超过就每轮都做 VACUUM 把文件压回去。
	// 正常使用（TTL 2 小时 + 条数上限）远达不到这个量，只有异常累积才会触发。
	return g.StartStateAutoCompact(6*time.Hour, 128<<20)
}
