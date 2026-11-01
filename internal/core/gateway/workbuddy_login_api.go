package gateway

import (
	"net/http"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/workbuddy"
)

// WorkBuddy 登录：copilot.tencent.com auth/state → 浏览器登录 → auth/token 轮询。
//
//	POST /api/provider/workbuddy/device          {"site":"workbuddy-cn"}
//	GET  /api/provider/workbuddy/device/{id}     → {state, login_url, interval, account}
//	POST /api/provider/workbuddy/device/{id}/cancel

func (g *Gateway) handleWorkBuddyDeviceStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Site string `json:"site"`
	}
	_ = decodeBody(r, &body)
	site := body.Site
	if site == "" {
		site = "workbuddy-cn"
	}
	s, err := workbuddy.StartLogin(site)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	eventbus.Emit(eventbus.TopicAccount+".auth_started", map[string]any{
		"provider_id": site, "session_id": s.ID, "method": "workbuddy_portal",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "login_url": s.LoginURL,
		"interval": s.Interval, "expires_in": s.ExpiresAt - s.CreatedAt,
	})
}

func (g *Gateway) handleWorkBuddyDevicePoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s := workbuddy.GetLogin(id)
	if s == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "登录会话不存在或已过期"})
		return
	}
	account := s.Account
	if s.State == "done" && account == "" {
		if creds := workbuddy.Credentials(id); creds != nil {
			label := "WorkBuddy 登录"
			if p, ok := provider.Get(s.ProviderID); ok {
				label = p.Manifest().DisplayName + " 登录"
			}
			env, apiErr := g.createAccount(s.ProviderID, label, creds, "")
			if apiErr != nil {
				writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
				return
			}
			if env != nil && workbuddy.MarkCommitted(id, env.AccountID) {
				account = env.AccountID
				eventbus.Emit(eventbus.TopicAccount+".added", map[string]any{
					"provider_id": s.ProviderID, "account_id": env.AccountID, "method": "workbuddy_portal",
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "error": s.Err,
		"login_url": s.LoginURL, "interval": s.Interval, "account": account,
	})
}

func (g *Gateway) handleWorkBuddyDeviceCancel(w http.ResponseWriter, r *http.Request) {
	workbuddy.CancelLogin(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}
