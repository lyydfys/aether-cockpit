package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/auth"
	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// handleAuthStart 启动一次认证（PKCE 或设备码）。
//
//	POST /api/auth/start
//	{"provider_id":"xxx","method":"oauth_pkce"}
func (g *Gateway) handleAuthStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProviderID string              `json:"provider_id"`
		Method     provider.AuthMethod `json:"method"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}

	p, ok := provider.Get(body.ProviderID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "渠道未注册: " + body.ProviderID})
		return
	}
	m := p.Manifest()

	cfg := m.OAuth
	// 允许 spec 覆盖 OAuth 参数：上游换了 client_id 或端点时不用改代码。
	if sp := spec.Load(body.ProviderID); sp != nil {
		cfg = mergeOAuthFromSpec(cfg, sp)
	}
	if cfg == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "该渠道未声明 OAuth 参数（Manifest.OAuth 为空），请改用手动凭证或本机导入",
		})
		return
	}

	coord := g.authCoord()
	var (
		sess *auth.Session
		err  error
	)
	switch body.Method {
	case provider.AuthOAuthPKCE, "":
		sess, err = coord.StartPKCE(body.ProviderID, cfg)
	case provider.AuthDeviceCode:
		sess, err = coord.StartDeviceCode(body.ProviderID, cfg)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "不支持的认证方式: " + string(body.Method)})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	eventbus.Emit(eventbus.TopicAccount+".auth_started", map[string]any{
		"provider_id": body.ProviderID,
		"session_id":  sess.ID,
		"method":      sess.Method,
	})

	writeJSON(w, http.StatusOK, map[string]any{"session": sess})
}

// handleAuthGet 查询认证会话。
func (g *Gateway) handleAuthGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s := g.authCoord().Get(id)
	if s == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "会话不存在或已过期"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": s})
}

// handleAuthCancel 取消认证。
func (g *Gateway) handleAuthCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g.authCoord().Cancel(id)
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": true})
}

// handleAuthCallback POST /api/auth/{id}/callback
//
// 浏览器无法回调本机时，用户粘贴完整回调地址（或裸 code）完成授权。
func (g *Gateway) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		CallbackURL string `json:"callback_url"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if err := g.authCoord().SubmitPKCECallback(id, strings.TrimSpace(body.CallbackURL)); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	s := g.authCoord().Get(id)
	if s == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "会话不存在或已过期"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": s})
}

// handleAuthCommit 把认证成功的凭证落成账号。
//
// 为什么要单独一步而不是回调里直接建账号：
// 前端需要在用户确认（比如选一个备注名）之后再入库，而且失败时要能重试。
func (g *Gateway) handleAuthCommit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s := g.authCoord().Get(id)
	if s == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "会话不存在或已过期"})
		return
	}
	if s.Status != auth.StatusSuccess || s.Credential == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "认证尚未成功，当前状态: " + string(s.Status),
		})
		return
	}

	var body struct {
		Label           string `json:"label"`
		RebindAccountID string `json:"rebind_account_id"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	_ = json.Unmarshal(raw, &body)

	// 原位重新授权：替换既有账号的凭证，不新建账号。
	if body.RebindAccountID != "" {
		if g.pool == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "账号池未初始化"})
			return
		}
		env, ok := g.pool.Get(body.RebindAccountID)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在: " + body.RebindAccountID})
			return
		}
		if env.ProviderID != s.ProviderID {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "授权渠道与目标账号不一致: " + s.ProviderID + " ≠ " + env.ProviderID,
			})
			return
		}
		g.replaceAccountCredentials(env, s.Credential)
		writeJSON(w, http.StatusOK, map[string]any{"account": env.Snapshot(), "rebound": true})
		return
	}

	label := body.Label
	if label == "" {
		label = s.Credential.Label
	}

	env, apiErr := g.createAccount(s.ProviderID, label, s.Credential.Credentials, "")
	if apiErr != nil {
		writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
		return
	}

	eventbus.Emit(eventbus.TopicAccount+".added", map[string]any{
		"provider_id": s.ProviderID,
		"account_id":  env.AccountID,
		"method":      s.Method,
	})
	writeJSON(w, http.StatusOK, map[string]any{"account": env.Snapshot()})
}

// handleLocalScan 扫描本机已登录客户端的凭证。
func (g *Gateway) handleLocalScan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := provider.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "渠道未注册: " + id})
		return
	}
	cap, ok := p.(provider.LocalImportCapability)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "该渠道未实现 LocalImportCapability，无法扫描本机凭证",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	found, err := cap.DiscoverLocalAccounts(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	// 扫描结果只返回展示信息，不回传凭证本身。
	// 凭证只在用户确认后的 local-import 步骤重新扫描并直接入库，
	// 避免浏览器侧长期持有 access_token / refresh_token。
	type foundView struct {
		Label          string   `json:"label"`
		Source         string   `json:"source,omitempty"`
		ExpiresAt      int64    `json:"expires_at,omitempty"`
		CredentialKeys []string `json:"credential_keys,omitempty"`
	}
	views := make([]foundView, 0, len(found))
	for _, d := range found {
		keys := make([]string, 0, len(d.Credentials))
		for k := range d.Credentials {
			keys = append(keys, k)
		}
		views = append(views, foundView{
			Label: d.Label, Source: d.Source, ExpiresAt: d.ExpiresAt, CredentialKeys: keys,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": views, "count": len(views)})
}

// handleLocalImport 把扫描到的凭证直接入库。
//
//	POST /api/providers/{id}/local-import
//	{"label":"主账号"}   —— 不传 label 则用扫描结果里的 label
func (g *Gateway) handleLocalImport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, ok := provider.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "渠道未注册: " + id})
		return
	}
	cap, ok := p.(provider.LocalImportCapability)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "该渠道不支持本机导入"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	found, err := cap.DiscoverLocalAccounts(ctx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if len(found) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"imported": 0, "message": "没有扫描到可用的登录凭证"})
		return
	}

	imported := make([]any, 0, len(found))
	for _, draft := range found {
		env, apiErr := g.createAccount(id, draft.Label, draft.Credentials, "")
		if apiErr != nil {
			continue
		}
		// 导入时能拿到真实指纹就用真实的，拿不到就用派生的。
		if draft.Fingerprint != nil {
			env.Fingerprint = *draft.Fingerprint
			if client, err := g.tr.NewClient(env.NetworkPolicy, "", env.TLSProfile); err == nil {
				env.SetHTTPClient(client)
			}
			g.pool.Add(env) // 覆盖回池
		}
		imported = append(imported, env.Snapshot())
	}

	eventbus.Emit(eventbus.TopicAccount+".imported", map[string]any{
		"provider_id": id,
		"count":       len(imported),
	})
	writeJSON(w, http.StatusOK, map[string]any{"imported": len(imported), "accounts": imported})
}

// handleTokenRefresh 手动刷新某账号：能续期凭证就续期，然后同步读一次额度。
//
// 积分制渠道（Trae / WorkBuddy）没有 token 续期能力，但用户点刷新按钮的
// 主要目的就是重读额度，所以不能因为"不能续期"而整单失败。
func (g *Gateway) handleTokenRefresh(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	env, ok := g.pool.Get(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在: " + id})
		return
	}
	p, ok := provider.Get(env.ProviderID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "渠道未注册"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	refreshed := false
	var credErr error
	refreshSupported := g.canRefreshCredential(env, p)
	if refreshSupported {
		if _, err := g.RefreshAccount(ctx, env, p); err != nil {
			// 渠道支持"额度刷新"但不支持"凭据续期"（例如 OpenCode Go 的
			// API Key）：按钮实际只刷额度，不要把能力缺失报成失败。
			if ae := ir.AsAPIError(err); ae != nil && ae.Code == ir.ErrCodeUnsupported {
				refreshSupported = false
			} else {
				credErr = err
			}
		} else {
			refreshed = true
			// 刷新结果必须立即落盘：只靠周期自动保存，进程在保存前退出会丢新令牌。
			g.saveAccountsNow()
			eventbus.Emit(eventbus.TopicAccount+".refreshed", map[string]any{
				"account_id":  env.AccountID,
				"provider_id": env.ProviderID,
			})
		}
	}

	var quotaErr error
	quotaSupported := g.providerQuotaReadable(env.ProviderID)
	if quotaSupported {
		quotaErr = g.readQuotaFor(env, 20*time.Second)
	}

	out := map[string]any{
		"refreshed":         refreshed,
		"refresh_supported": refreshSupported,
		"quota":             env.Snapshot().Quota,
		"quota_supported":   quotaSupported,
		"quota_read":        quotaSupported && quotaErr == nil,
	}
	if credErr != nil {
		out["warning"] = "凭据续期失败：" + credErr.Error()
	}
	if quotaErr != nil {
		out["quota_error"] = quotaErr.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// canRefreshCredential 判断刷新凭证是否会真的发出请求。
// 不能直接调 RefreshAccount 试探：它在缺少 refresh_token 时会把账号标失效。
func (g *Gateway) canRefreshCredential(env *provider.AccountEnv, p provider.BaseProvider) bool {
	if _, ok := p.(provider.TokenRefreshCapability); ok {
		return true
	}
	cfg := p.Manifest().OAuth
	if sp := spec.Load(env.ProviderID); sp != nil {
		cfg = mergeOAuthFromSpec(cfg, sp)
	}
	if cfg == nil || cfg.TokenURL == "" {
		return false
	}
	field := cfg.RefreshField
	if field == "" {
		field = "refresh_token"
	}
	return strings.TrimSpace(env.GetCredential(field)) != ""
}

// RefreshAccount 刷新一个账号的凭证。供 API 与定时刷新共用。
//
// 顺序：
//  1. 渠道自己实现了 TokenRefreshCapability -> 优先用（它最了解自己的刷新语义）
//  2. 否则走标准 OAuth refresh_token
func (g *Gateway) RefreshAccount(ctx context.Context, env *provider.AccountEnv, p provider.BaseProvider) (*provider.CredentialDraft, error) {
	if cap, ok := p.(provider.TokenRefreshCapability); ok {
		draft, err := cap.RefreshCredential(ctx, env)
		if err != nil {
			return nil, err
		}
		g.applyCredential(env, draft)
		return draft, nil
	}

	cfg := p.Manifest().OAuth
	if sp := spec.Load(env.ProviderID); sp != nil {
		cfg = mergeOAuthFromSpec(cfg, sp)
	}
	if cfg == nil || cfg.TokenURL == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUnsupported,
			"账号所属渠道既未实现刷新能力，也未声明 OAuth token 端点")
	}

	refreshField := cfg.RefreshField
	if refreshField == "" {
		refreshField = "refresh_token"
	}
	rt := env.GetCredential(refreshField)
	if rt == "" {
		env.MarkInvalid("没有 refresh_token，需要重新登录")
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "没有 refresh_token，需要重新登录")
	}

	draft, err := g.authCoord().RefreshToken(cfg, rt)
	if err != nil {
		return nil, err
	}
	g.applyCredential(env, draft)
	return draft, nil
}

// applyCredential 把刷新得到的凭证写回账号。
func (g *Gateway) applyCredential(env *provider.AccountEnv, draft *provider.CredentialDraft) {
	if draft == nil {
		return
	}
	for k, v := range draft.Credentials {
		env.SetCredential(k, v)
	}
	env.MarkReady() // 刷新成功意味着凭证重新可用
	eventbus.Emit(eventbus.TopicAccount+".credential_updated", map[string]any{
		"account_id": env.AccountID,
	})
}

// --- 辅助 ---

// authCoord 返回认证协调器（懒初始化）。
func (g *Gateway) authCoord() *auth.Coordinator {
	g.authOnce.Do(func() {
		g.authC = auth.New()
	})
	return g.authC
}

// mergeOAuthFromSpec 允许 spec 覆盖 OAuth 参数。
//
// 典型用途：上游换了 client_id 或把 token 端点迁了，改 JSON 即可，不用发版。
// 合并顺序：内置 Manifest < spec.oauth < spec.endpoints。
func mergeOAuthFromSpec(base *provider.OAuthConfig, sp *spec.Spec) *provider.OAuthConfig {
	if sp == nil {
		return base
	}
	if base == nil {
		base = &provider.OAuthConfig{}
	}
	out := *base
	out.Scopes = append([]string(nil), base.Scopes...)
	if len(base.ExtraAuthParams) > 0 {
		out.ExtraAuthParams = make(map[string]string, len(base.ExtraAuthParams))
		for k, v := range base.ExtraAuthParams {
			out.ExtraAuthParams[k] = v
		}
	}

	if o := sp.OAuth; o != nil {
		if o.AuthorizeURL != "" {
			out.AuthorizeURL = o.AuthorizeURL
		}
		if o.TokenURL != "" {
			out.TokenURL = o.TokenURL
		}
		if o.DeviceCodeURL != "" {
			out.DeviceCodeURL = o.DeviceCodeURL
		}
		if o.ClientID != "" {
			out.ClientID = o.ClientID
		}
		if o.ClientSecret != "" {
			out.ClientSecret = o.ClientSecret
		}
		if len(o.Scopes) > 0 {
			out.Scopes = append([]string(nil), o.Scopes...)
		}
		if o.RedirectPath != "" {
			out.RedirectPath = o.RedirectPath
		}
		if out.ExtraAuthParams == nil && len(o.ExtraAuthParams) > 0 {
			out.ExtraAuthParams = make(map[string]string, len(o.ExtraAuthParams))
		}
		for k, v := range o.ExtraAuthParams {
			out.ExtraAuthParams[k] = v
		}
		if o.TokenField != "" {
			out.TokenField = o.TokenField
		}
		if o.RefreshField != "" {
			out.RefreshField = o.RefreshField
		}
		if o.ExpiresField != "" {
			out.ExpiresField = o.ExpiresField
		}
	}

	if sp.BaseURL != "" && out.TokenURL == "" {
		out.TokenURL = strings.TrimRight(sp.BaseURL, "/") + "/oauth/token"
	}
	if v, ok := sp.Endpoints["authorize"]; ok && v != "" {
		out.AuthorizeURL = v
	}
	if v, ok := sp.Endpoints["token"]; ok && v != "" {
		out.TokenURL = v
	}
	if v, ok := sp.Endpoints["device_code"]; ok && v != "" {
		out.DeviceCodeURL = v
	}
	return &out
}
