package gateway

import (
	"net/http"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/trae"
)

// Trae 登录：/authorization + 本地回调 + ExchangeToken。
//
//	POST /api/provider/trae/device          {"site":"trae-cn"}
//	GET  /api/provider/trae/device/{id}
//	POST /api/provider/trae/device/{id}/cancel

func (g *Gateway) handleTraeDeviceStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Site string `json:"site"`
	}
	_ = decodeBody(r, &body)
	site := body.Site
	if site == "" {
		site = "trae-cn"
	}
	s, err := trae.StartLogin(site)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	eventbus.Emit(eventbus.TopicAccount+".auth_started", map[string]any{
		"provider_id": site, "session_id": s.ID, "method": "trae_portal",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "login_url": s.LoginURL,
		"interval": s.Interval, "expires_in": s.ExpiresAt - s.CreatedAt,
	})
}

func (g *Gateway) handleTraeDevicePoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s := trae.GetLogin(id)
	if s == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "登录会话不存在或已过期"})
		return
	}
	account := s.Account
	if s.State == "done" && account == "" {
		if creds := trae.Credentials(id); creds != nil {
			label := "Trae 登录"
			if p, ok := provider.Get(s.ProviderID); ok {
				label = p.Manifest().DisplayName + " 登录"
			}
			env, apiErr := g.createAccount(s.ProviderID, label, creds, "")
			if apiErr != nil {
				writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
				return
			}
			if env != nil && trae.MarkCommitted(id, env.AccountID) {
				account = env.AccountID
				eventbus.Emit(eventbus.TopicAccount+".added", map[string]any{
					"provider_id": s.ProviderID, "account_id": env.AccountID, "method": "trae_portal",
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "error": s.Err,
		"login_url": s.LoginURL, "interval": s.Interval, "account": account,
	})
}

func (g *Gateway) handleTraeDeviceCancel(w http.ResponseWriter, r *http.Request) {
	trae.CancelLogin(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}
