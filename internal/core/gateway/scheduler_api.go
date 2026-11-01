package gateway

import (
	"net/http"
	"strconv"

	"aether-cockpit/internal/core/scheduler"
	"aether-cockpit/internal/provider"
)

// handleScheduler 返回调度器运行状态与全部任务快照。
func (g *Gateway) handleScheduler(w http.ResponseWriter, r *http.Request) {
	s := g.sched.Load()
	if s == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": false,
			"reason":  "调度器未启动",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true,
		"info":    s.Info(),
		"jobs":    s.Status(),
	})
}

// handleSchedulerSummary GET /api/scheduler/checkin-summary?since=<unix>
func (g *Gateway) handleSchedulerSummary(w http.ResponseWriter, r *http.Request) {
	s := g.sched.Load()
	if s == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "调度器未启动"})
		return
	}
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	writeJSON(w, http.StatusOK, s.CheckinSummary(since))
}

// handleSchedulerRun 手动触发全部签到。
//
// 注意：这里是"入队"而非"执行完成"，因为真正的执行走串行抖动队列，
// 一个账号一个账号地打，不可能在 HTTP 请求周期内做完。
func (g *Gateway) handleSchedulerRun(w http.ResponseWriter, r *http.Request) {
	s := g.sched.Load()
	if s == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "调度器未启动"})
		return
	}
	queued, skipped := s.RunAllDetailed("manual")
	note := "任务已入队，将按渠道串行执行并插入 2~6 秒随机间隔"
	switch {
	case queued == 0 && skipped > 0:
		note = "今天已经完成，本次跳过 " + strconv.Itoa(skipped) + " 个账号"
	case queued == 0 && skipped == 0:
		note = "当前没有可入队的账号"
	case skipped > 0:
		note = "已入队 " + strconv.Itoa(queued) + " 个账号；今日已完成 " + strconv.Itoa(skipped) + " 个，已跳过"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"queued": queued, "skipped": skipped, "note": note,
	})
}

// handleSchedulerRunProvider 触发某渠道的全部签到任务。
func (g *Gateway) handleSchedulerRunProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s := g.sched.Load()
	if s == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "调度器未启动"})
		return
	}
	if _, ok := provider.Get(id); !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "渠道未注册: " + id})
		return
	}
	queued, skipped := s.RunProviderDetailed(id, "manual")
	note := ""
	switch {
	case queued == 0 && skipped > 0:
		note = "今天已经完成，本次跳过 " + strconv.Itoa(skipped) + " 个账号"
	case queued == 0 && skipped == 0:
		note = "当前没有可入队的账号"
	case skipped > 0:
		note = "已入队 " + strconv.Itoa(queued) + " 个账号；今日已完成 " + strconv.Itoa(skipped) + " 个，已跳过"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"queued": queued, "skipped": skipped, "provider_id": id, "note": note,
	})
}

// handleQuotaRefresh 手动触发一次额度刷新。
//
// 异步执行：刷新要串行跨所有账号（含抖动），不可能在请求周期内完成。
func (g *Gateway) handleQuotaRefresh(w http.ResponseWriter, r *http.Request) {
	s := g.sched.Load()
	if s == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "调度器未启动"})
		return
	}
	s.RefreshQuotasNow()
	writeJSON(w, http.StatusOK, map[string]any{
		"started": true,
		"note":    "已在后台串行刷新全部账号额度，间隔 0.8~2 秒",
	})
}

// checkinToday 汇总某账号今日的签到结果，供账号列表展示。
func (g *Gateway) checkinToday(accountID string) map[string]scheduler.Result {
	s := g.sched.Load()
	if s == nil {
		return nil
	}
	res := s.State().TodayResults(accountID)
	if len(res) == 0 {
		return nil
	}
	return res
}
