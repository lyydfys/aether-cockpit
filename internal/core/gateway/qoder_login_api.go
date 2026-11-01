package gateway

import (
	"net/http"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/qoder"
)

// Qoder 订阅登录：设备选择页 + 轮询（非标准 RFC 8628，见 qoder/login.go）。
//
//	POST /api/provider/qoder/device            {"site":"qoder-cn"}
//	GET  /api/provider/qoder/device/{id}       → {state, login_url, interval, account}
//	POST /api/provider/qoder/device/{id}/cancel

// handleQoderDeviceStart 启动一次 Qoder 登录。
func (g *Gateway) handleQoderDeviceStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Site string `json:"site"`
	}
	_ = decodeBody(r, &body)
	site := body.Site
	if site == "" {
		site = "qoder-cn"
	}
	s, err := qoder.StartPortalLogin(site)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	eventbus.Emit(eventbus.TopicAccount+".auth_started", map[string]any{
		"provider_id": site, "session_id": s.ID, "method": "qoder_portal",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "login_url": s.LoginURL,
		"interval": s.Interval, "expires_in": s.ExpiresAt - s.CreatedAt,
	})
}

// handleQoderDevicePoll 查询会话；完成时把账号入库一次。
func (g *Gateway) handleQoderDevicePoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s := qoder.GetPortalLogin(id)
	if s == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "登录会话不存在或已过期"})
		return
	}
	account := s.Account
	if s.State == "done" && account == "" {
		if creds := qoder.PortalCredentials(id); creds != nil {
			label := "Qoder 登录"
			if p, ok := provider.Get(s.ProviderID); ok {
				label = p.Manifest().DisplayName + " 登录"
			}
			env, apiErr := g.createAccount(s.ProviderID, label, creds, "")
			if apiErr != nil {
				writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
				return
			}
			if env != nil && qoder.MarkPortalCommitted(id, env.AccountID) {
				account = env.AccountID
				eventbus.Emit(eventbus.TopicAccount+".added", map[string]any{
					"provider_id": s.ProviderID, "account_id": env.AccountID, "method": "qoder_portal",
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": s.ID, "state": s.State, "error": s.Err,
		"login_url": s.LoginURL, "interval": s.Interval, "account": account,
	})
}

// handleQoderDeviceCancel 取消登录。
func (g *Gateway) handleQoderDeviceCancel(w http.ResponseWriter, r *http.Request) {
	qoder.CancelPortalLogin(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}
