package gateway

import (
	"net/http"

	"aether-cockpit/internal/core/rtstats"
)

// handleRoutingStats GET /api/routing-stats
//
// 返回各渠道的"后端族群"分布：官方 / 中转 / 未知 + 官方占比。
//
// 口径必须说清楚：这是按**响应 id 前缀**推定的，不是上游承认的 SLA。
// OpenCode 这类混合池在 v2 之后会夹掉非法参数，客户端已经无法用行为探针
// 判断"这一跳是不是官方"，id 前缀是最后一个还活着的信号。
func (g *Gateway) handleRoutingStats(w http.ResponseWriter, r *http.Request) {
	all := rtstats.All()
	providers := make(map[string]any, len(all))
	for id, c := range all {
		providers[id] = map[string]any{
			"total":    c.Total,
			"official": c.Official,
			"relay":    c.Relay,
			"unknown":  c.Unknown,
			// share 是 0..1 的小数，前端自己决定怎么显示百分比。
			"share":      c.Share(),
			"updated_at": c.UpdatedAt,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": providers,
		"note":      "按响应 id 前缀推定：chatcmpl-/纯数字=官方，gw-=中转；仅作趋势参考",
	})
}
