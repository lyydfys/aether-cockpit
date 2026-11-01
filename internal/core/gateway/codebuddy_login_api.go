package gateway

import (
	"net/http"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/codebuddy"
)

// CodeBuddy 登录（国际/国内）：{base}/v2/plugin/auth/state → 登录页 → auth/token 轮询。
//
//	POST /api/provider/codebuddy/device          {"site":"codebuddy-intl"}
//	GET  /api/provider/codebuddy/device/{id}
//	POST /api/provider/codebuddy/device/{id}/cancel

func (g *Gateway) handleCodeBuddyDeviceStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Site string `json:"site"`
	}
	_ = decodeBody(r, &body)
	site := body.Site
	if site == "" {
		site = "codebuddy-intl"
	}
	s, err := codebuddy.StartLogin(site)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	eventbus.Emit(eventbus.TopicAccount+".auth_started", map[string]any{
		"provider_id": site, "session_id": s.ID, "method": "codebuddy_portal",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "login_url": s.LoginURL,
		"interval": s.Interval, "expires_in": s.ExpiresAt - s.CreatedAt,
	})
}

func (g *Gateway) handleCodeBuddyDevicePoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s := codebuddy.GetLogin(id)
	if s == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "登录会话不存在或已过期"})
		return
	}
	account := s.Account
	if s.State == "done" && account == "" {
		if creds := codebuddy.Credentials(id); creds != nil {
			label := "CodeBuddy 登录"
			if p, ok := provider.Get(s.ProviderID); ok {
				label = p.Manifest().DisplayName + " 登录"
			}
			env, apiErr := g.createAccount(s.ProviderID, label, creds, "")
			if apiErr != nil {
				writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
				return
			}
			if env != nil && codebuddy.MarkCommitted(id, env.AccountID) {
				account = env.AccountID
				eventbus.Emit(eventbus.TopicAccount+".added", map[string]any{
					"provider_id": s.ProviderID, "account_id": env.AccountID, "method": "codebuddy_portal",
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "error": s.Err,
		"login_url": s.LoginURL, "interval": s.Interval, "account": account,
	})
}

func (g *Gateway) handleCodeBuddyDeviceCancel(w http.ResponseWriter, r *http.Request) {
	codebuddy.CancelLogin(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}
