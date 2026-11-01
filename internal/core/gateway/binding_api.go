package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
)

// 官方授权绑定管理：查询账号绑定到哪个官方账号，并支持"原位重新授权"。
//
// 重新授权复用通用 OAuth 流程（/api/auth/start → /api/auth/{id}/commit）；
// commit 时带 rebind_account_id 表示替换该账号的凭证，而不是新建账号。

// handleAccountBinding GET /api/accounts/{id}/binding
func (g *Gateway) handleAccountBinding(w http.ResponseWriter, r *http.Request) {
	if g.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "账号池未初始化"})
		return
	}
	id := r.PathValue("id")
	env, ok := g.pool.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在: " + id})
		return
	}
	p, ok := provider.Get(env.ProviderID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "渠道未注册: " + env.ProviderID})
		return
	}
	cap, ok := p.(provider.OfficialBindingCapability)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "该渠道不支持官方绑定信息"})
		return
	}
	binding := cap.OfficialBinding(env.Credentials)
	paused, reason, at := env.OfficialPaused()
	writeJSON(w, http.StatusOK, map[string]any{
		"account_id":   env.AccountID,
		"provider_id":  env.ProviderID,
		"binding":      binding,
		"official":     g.isOfficialAccount(env),
		"paused":       paused,
		"pause_reason": reason,
		"paused_at":    at.Unix(),
	})
}

// replaceAccountCredentials 用一次成功授权的结果替换账号凭证（原位重绑）。
// 空值不覆盖旧值：授权响应可能只带部分字段，清空会把可用账号写坏。
func (g *Gateway) replaceAccountCredentials(env *provider.AccountEnv, draft *provider.CredentialDraft) {
	if env == nil || draft == nil {
		return
	}
	for k, v := range draft.Credentials {
		if strings.TrimSpace(v) == "" {
			continue
		}
		env.SetCredential(k, v)
	}
	env.MarkSuccess()
	g.saveAccountsNow()
	eventbus.Emit(eventbus.TopicAccount+".rebound", map[string]any{
		"account_id":  env.AccountID,
		"provider_id": env.ProviderID,
	})
}

// isOfficialAccount 判断账号是否官方订阅登录态（由渠道实现回答）。
func (g *Gateway) isOfficialAccount(env *provider.AccountEnv) bool {
	if env == nil {
		return false
	}
	p, ok := provider.Get(env.ProviderID)
	if !ok {
		return false
	}
	cap, ok := p.(provider.OfficialAccountCapability)
	return ok && cap.IsOfficialAccount(env.Credentials)
}

// handleOfficialPause POST /api/accounts/{id}/official/pause
//
// 手动暂停官方通道：账号立即退出路由，直到恢复或检查授权通过。
func (g *Gateway) handleOfficialPause(w http.ResponseWriter, r *http.Request) {
	env, ok := g.officialEnv(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&body)
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		reason = "手动暂停"
	}
	env.SetOfficialPause(reason)
	g.saveAccountsNow()
	eventbus.Emit(eventbus.TopicAccount+".state_changed", env.Snapshot())
	writeJSON(w, http.StatusOK, map[string]any{"paused": true, "reason": reason})
}

// handleOfficialResume POST /api/accounts/{id}/official/resume
func (g *Gateway) handleOfficialResume(w http.ResponseWriter, r *http.Request) {
	env, ok := g.officialEnv(w, r)
	if !ok {
		return
	}
	env.ResumeOfficial()
	env.MarkSuccess()
	g.saveAccountsNow()
	eventbus.Emit(eventbus.TopicAccount+".state_changed", env.Snapshot())
	writeJSON(w, http.StatusOK, map[string]any{"paused": false})
}

// handleOfficialCheck POST /api/accounts/{id}/official/check
//
// 用渠道的额度接口验证官方授权是否仍有效（不生成回复、不消耗对话额度）：
// 成功即恢复官方通道；失败保持暂停并返回原因。
func (g *Gateway) handleOfficialCheck(w http.ResponseWriter, r *http.Request) {
	env, ok := g.officialEnv(w, r)
	if !ok {
		return
	}
	p, _ := provider.Get(env.ProviderID)
	quota, canQuota := p.(provider.QuotaCapability)
	if !canQuota {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "该渠道没有可用于校验授权状态的接口"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if _, err := quota.QueryQuota(ctx, env); err != nil {
		env.SetOfficialPause("检查授权失败：" + err.Error())
		g.saveAccountsNow()
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "paused": true, "error": err.Error()})
		return
	}
	env.ResumeOfficial()
	env.MarkSuccess()
	g.saveAccountsNow()
	eventbus.Emit(eventbus.TopicAccount+".state_changed", env.Snapshot())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "paused": false})
}

// officialEnv 解析路径里的账号并校验"官方订阅"语义。
func (g *Gateway) officialEnv(w http.ResponseWriter, r *http.Request) (*provider.AccountEnv, bool) {
	if g.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "账号池未初始化"})
		return nil, false
	}
	env, ok := g.pool.Get(r.PathValue("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在"})
		return nil, false
	}
	if !g.isOfficialAccount(env) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "该账号不是官方订阅登录态，没有官方通道可管理"})
		return nil, false
	}
	return env, true
}
