package gateway

import (
	"net/http"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/zed"
)

// Zed 登录：native_app_signin + 本地 RSA 回环回调。
//
//	POST /api/provider/zed/device
//	GET  /api/provider/zed/device/{id}
//	POST /api/provider/zed/device/{id}/cancel

func (g *Gateway) handleZedDeviceStart(w http.ResponseWriter, r *http.Request) {
	s, err := zed.StartLogin()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	eventbus.Emit(eventbus.TopicAccount+".auth_started", map[string]any{
		"provider_id": "zed", "session_id": s.ID, "method": "zed_native_signin",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "login_url": s.LoginURL,
		"interval": s.Interval, "expires_in": s.ExpiresAt - s.CreatedAt,
	})
}

func (g *Gateway) handleZedDevicePoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s := zed.GetLogin(id)
	if s == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "登录会话不存在或已过期"})
		return
	}
	account := s.Account
	if s.State == "done" && account == "" {
		if creds := zed.Credentials(id); creds != nil {
			label := "Zed 登录"
			if p, ok := provider.Get("zed"); ok {
				label = p.Manifest().DisplayName + " 登录"
			}
			env, apiErr := g.createAccount("zed", label, creds, "")
			if apiErr != nil {
				writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
				return
			}
			if env != nil && zed.MarkCommitted(id, env.AccountID) {
				account = env.AccountID
				eventbus.Emit(eventbus.TopicAccount+".added", map[string]any{
					"provider_id": "zed", "account_id": env.AccountID, "method": "zed_native_signin",
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "error": s.Err,
		"login_url": s.LoginURL, "interval": s.Interval, "account": account,
	})
}

func (g *Gateway) handleZedDeviceCancel(w http.ResponseWriter, r *http.Request) {
	zed.CancelLogin(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}
