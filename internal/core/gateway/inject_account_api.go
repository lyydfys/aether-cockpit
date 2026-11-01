package gateway

import (
	"context"
	"net/http"
	"time"

	"aether-cockpit/internal/provider"
)

// 一键切号：把渠道账号写回本机 IDE 的登录态。
//
// 与「本机导入」互逆：导入是 IDE → 网关，这里是 网关 → IDE。
// 目前落点是 Antigravity（Windows 凭据 gemini:antigravity + jetski 回退文件）；
// 渠道通过 LocalInjectCapability 自行决定写哪里，网关只负责取账号与鉴权。
func (g *Gateway) handleInjectAccount(w http.ResponseWriter, r *http.Request) {
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
	injector, ok := provider.AsLocalInject(p)
	if !ok {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "该渠道不支持写入本机 IDE（实现 LocalInjectCapability 后可一键切号）",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	// 过期/临期的 token 先续期再写：IDE 拿到死 token 会直接显示登录失效，
	// 而重启 IDE 的成本远高于在这里多一次刷新。
	if injectShouldRefresh(env) {
		if _, err := g.RefreshAccount(ctx, env, p); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": "账号续期失败，未写入本机 IDE：" + err.Error(),
			})
			return
		}
		g.saveAccountsNow()
	}

	res, err := injector.InjectLocalAccount(ctx, env)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"injected": true,
		"account":  env.AccountID,
		"result":   res,
	})
}

// injectShouldRefresh 判断写回前是否需要先续期。
func injectShouldRefresh(env *provider.AccountEnv) bool {
	if env == nil || env.Credentials == nil {
		return false
	}
	if env.GetCredential("refresh_token") == "" {
		return false
	}
	expiresAt := env.GetCredential("expires_at")
	if expiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return false
	}
	return time.Now().After(t.Add(-2 * time.Minute))
}
