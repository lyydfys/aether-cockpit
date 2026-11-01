package gateway

import (
	"context"
	"net/http"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/providers/windsurf"
)

// Windsurf / Devin 登录：邮箱密码 → auth1 → PostAuth/OTT/RegisterUser。
//
//	POST /api/provider/windsurf/login {"email":"","password":""} 或 {"auth1_token":""}
func (g *Gateway) handleWindsurfLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Auth1    string `json:"auth1_token"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()

	auth1 := body.Auth1
	email := body.Email
	if auth1 == "" {
		res, err := windsurf.LoginWithPassword(ctx, body.Email, body.Password)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		auth1 = res.Auth1Token
		if email == "" {
			email = res.Email
		}
	}
	full, err := windsurf.FullRefresh(ctx, auth1)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	creds := map[string]string{
		"auth1_token":   full.Auth1Token,
		"session_token": full.SessionToken,
		"ide_token":     full.IDEToken,
		"account_id":    full.AccountID,
		"org_id":        full.OrgID,
	}
	if email != "" {
		creds["email"] = email
	}
	label := "Windsurf 登录"
	if email != "" {
		label = "Windsurf (" + email + ")"
	}
	env, apiErr := g.createAccount("windsurf", label, creds, "")
	if apiErr != nil {
		writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
		return
	}
	eventbus.Emit(eventbus.TopicAccount+".added", map[string]any{
		"provider_id": "windsurf", "account_id": env.AccountID, "method": "windsurf_password",
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"account": env.Snapshot(), "email": email,
	})
}
