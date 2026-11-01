package gateway

import (
	"net/http"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/kiro"
)

// Kiro 登录：app.kiro.dev/signin + 本地回调 + Kiro oauth/token。
//
//	POST /api/provider/kiro/device
//	GET  /api/provider/kiro/device/{id}
//	POST /api/provider/kiro/device/{id}/cancel

func (g *Gateway) handleKiroDeviceStart(w http.ResponseWriter, r *http.Request) {
	s, err := kiro.StartLogin("kiro")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	eventbus.Emit(eventbus.TopicAccount+".auth_started", map[string]any{
		"provider_id": "kiro", "session_id": s.ID, "method": "kiro_portal",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "login_url": s.LoginURL,
		"interval": s.Interval, "expires_in": s.ExpiresAt - s.CreatedAt,
	})
}

func (g *Gateway) handleKiroDevicePoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s := kiro.GetLogin(id)
	if s == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "登录会话不存在或已过期"})
		return
	}
	account := s.Account
	if s.State == "done" && account == "" {
		if creds := kiro.Credentials(id); creds != nil {
			label := "Kiro 登录"
			if p, ok := provider.Get("kiro"); ok {
				label = p.Manifest().DisplayName + " 登录"
			}
			env, apiErr := g.createAccount("kiro", label, creds, "")
			if apiErr != nil {
				writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
				return
			}
			if env != nil && kiro.MarkCommitted(id, env.AccountID) {
				account = env.AccountID
				eventbus.Emit(eventbus.TopicAccount+".added", map[string]any{
					"provider_id": "kiro", "account_id": env.AccountID, "method": "kiro_portal",
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "error": s.Err,
		"login_url": s.LoginURL, "interval": s.Interval, "account": account,
	})
}

func (g *Gateway) handleKiroDeviceCancel(w http.ResponseWriter, r *http.Request) {
	kiro.CancelLogin(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}
