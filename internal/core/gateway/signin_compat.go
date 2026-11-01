package gateway

import (
	"encoding/json"
	"io"
	"net/http"
)

// aether 的订阅磁贴（SUBS）会调 /api/signin 走各家的网页登录。
// 本项目的订阅账号通过「导入本机登录态 / 手动凭证 / 兼容端点」接入，
// 不代做第三方网页登录；这里给出明确指引，而不是让按钮 404。

const signinUnsupported = "Aether Cockpit 不代做该渠道的网页登录；请在供应商页使用「导入本机登录态」或手动填写凭证"

// handleSigninCompat POST /api/signin
func (g *Gateway) handleSigninCompat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent string `json:"agent"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	writeJSON(w, http.StatusOK, map[string]any{
		"agent": body.Agent, "state": "failed", "error": signinUnsupported,
	})
}

// handleSigninGetCompat GET /api/signin/{id}
func (g *Gateway) handleSigninGetCompat(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"state": "failed", "error": signinUnsupported})
}

// handleSigninCancelCompat POST /api/signin/{id}/cancel
func (g *Gateway) handleSigninCancelCompat(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}

// handleSigninImportCompat POST /api/signin/import —— 文件导入也指向本机导入。
func (g *Gateway) handleSigninImportCompat(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error": "请在供应商页使用「导入本机配置」；Aether Cockpit 不从粘贴文件创建订阅账号",
	})
}
