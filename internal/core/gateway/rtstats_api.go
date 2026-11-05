package gateway

import (
	"net/http"

	"aether-cockpit/internal/core/rtstats"
)

// handleRoutingStats GET /api/routing-stats
//
// 返回各渠道的"后端族群"分布：官方 / 中转 / 未知 + 官方占比，并附**按模型**
// 的细分。必须给模型级：实测同一渠道里 flash 与 pro 会落在完全不同的后端，
// 渠道级聚合会把"某个模型 100% 官方、另一个 0%"平均成一个没意义的数字。
//
// 口径说清楚：按**响应 id 前缀**推定，不是上游承认的 SLA。
// OpenCode 这类混合池在 v2 之后会夹掉非法参数，页面上的百分比只作趋势参考。
func (g *Gateway) handleRoutingStats(w http.ResponseWriter, r *http.Request) {
	all := rtstats.All()
	byModel := rtstats.AllModels()

	counters := func(c rtstats.Counters) map[string]any {
		return map[string]any{
			"total":    c.Total,
			"official": c.Official,
			"relay":    c.Relay,
			"unknown":  c.Unknown,
			// share 是 0..1 的小数，前端自己决定怎么显示百分比。
			"share":      c.Share(),
			"updated_at": c.UpdatedAt,
		}
	}

	providers := make(map[string]any, len(all))
	for id, c := range all {
		entry := counters(c)
		if models := byModel[id]; len(models) > 0 {
			perModel := make(map[string]any, len(models))
			for model, mc := range models {
				perModel[model] = counters(mc)
			}
			entry["models"] = perModel
		}
		providers[id] = entry
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": providers,
		"note":      "按响应 id 前缀推定：chatcmpl-/纯数字=官方，gw-=中转；仅作趋势参考",
	})
}
