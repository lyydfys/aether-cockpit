package gateway

import (
	"encoding/json"
	"io"
	"net/http"
)

// 账号级并发上限与实时"车道"。
//
// 渠道编辑页的「Requests at once」药丸按 provider/accountconcurrency 改单个账号
// 的并发上限（null=跟随全局、0=不限制），并轮询 /api/lanes 显示此刻在跑多少。
// 我们没有排队队列：抢不到槽位的请求会换下一个候选，所以 waiting 恒为 0。

func (g *Gateway) handleProviderAccountConcurrency(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Account string `json:"account"`
		Limit   *int   `json:"limit"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if body.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少渠道 id"})
		return
	}
	acct := body.Account
	found := false
	for _, e := range g.pool.ListByProvider(body.ID) {
		if e.AccountID == acct || e.Label == acct {
			e.SetConcurrencyOverride(body.Limit)
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在: " + acct})
		return
	}
	g.saveAccountsNow()
	g.respondProviders(w, r)
}

// handleLanes GET /api/lanes —— 每个账号此刻的并发占用（键 "provider#account"）。
func (g *Gateway) handleLanes(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{}
	for _, e := range g.pool.All() {
		out[e.ProviderID+"#"+e.AccountID] = map[string]any{
			"busy": e.Inflight(), "waiting": g.laneWaiting(e.ProviderID),
		}
	}
	writeJSON(w, http.StatusOK, out)
}
