package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/scheduler"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/core/transport"
	"aether-cockpit/internal/provider"
)

// handleHealth 健康探针。
func (g *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	v, rev, _, _, _ := buildInfo()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"uptime_sec": int(time.Since(g.startedAt).Seconds()),
		"providers":  len(provider.All()),
		"accounts":   len(g.pool.All()),
		"version":    v,
		"revision":   rev,
	})
}

// handleProviders 下发全部渠道的 Manifest，前端据此渲染所有页面。
//
// 这是"新增渠道前端零改动"的落点：前端不认识任何具体渠道，
// 只认识 ProviderManifest 这个契约。
// balanceTakesToken 决定渠道编辑页是否展示「Account balance」区块。
//
// 有内置用量接口的渠道也要给：中继类（codex/opencode/openai-compat）的
// 账单接口经常不存在，用户需要用自定义 Balance URL 补上；没有用量接口的
// 渠道（Qoder 等）同样需要。只有真正有稳定官方用量口径的订阅（Claude/
// Antigravity/WorkBuddy/Trae/Kiro/MiniMax）保持隐藏，避免引导用户重复配置。
func balanceTakesToken(providerID string, hasQuota bool) bool {
	switch providerID {
	case "codex", "opencode", "openai-compat", "qoder-cn", "qoder-intl":
		return true
	}
	return !hasQuota
}

func (g *Gateway) handleProviders(w http.ResponseWriter, r *http.Request) {
	manifests := provider.AllManifests()
	// 接入向导需要看到“还没账号但已内置”的 vendor 渠道（xAI/DeepSeek/Kimi…）；
	// 渠道页仍按旧规则隐藏空 vendor 行，避免列表被占位渠道撑满。
	includeEmpty := r.URL.Query().Get("include_empty") == "1"

	// 附带每个渠道的账号统计，省一次请求。
	//
	// 字段同时满足两套命名：display_name/account_count 是内核原生的，
	// name/host/key/models/agents/ready 是前端（aether 改造而来）读的。
	// 合并在一处下发，避免前端为了一个字段再发一次请求。
	type providerView struct {
		provider.ProviderManifest
		AccountCount int `json:"account_count"`
		ReadyCount   int `json:"ready_count"`

		Name               string                       `json:"name"`
		Host               string                       `json:"host"`
		Key                aetherKeyView                `json:"key"`
		KeyList            []aetherKeyEntry             `json:"keyList"`
		AccountModels      map[string][]string          `json:"accountModels,omitempty"`
		AccountMappings    map[string]map[string]string `json:"accountModelMapping,omitempty"`
		AccountBalances    map[string]map[string]any    `json:"accountBalances,omitempty"`
		AccountConcurrency map[string]int               `json:"accountConcurrency,omitempty"`
		Agents             []any                        `json:"agents"`
		Ready              bool                         `json:"ready"`
		Off                bool                         `json:"off"`
		// AutoOff 是"账号全部失效/耗尽被自动下线"（与手动 Off 分开）。
		// 前端据此显示"自动关闭 · 原因 · 恢复时间"，并允许一键手动开回来。
		AutoOff            bool              `json:"auto_off,omitempty"`
		AutoOffReason      string            `json:"auto_off_reason,omitempty"`
		AutoOffUntil       int64             `json:"auto_off_until,omitempty"`
		Draws              int               `json:"draws"`
		Routing            string            `json:"routing,omitempty"`
		Sink               bool              `json:"sink,omitempty"`
		Affinity           string            `json:"affinity,omitempty"`
		NoModels           bool              `json:"noModels"`
		ModelsSource       string            `json:"models_source,omitempty"`
		ModelsFetched      int64             `json:"models_fetched,omitempty"`
		ModelsMode         string            `json:"models_mode,omitempty"`
		ModelListSupported bool              `json:"models_live_supported,omitempty"`
		ModelsView         []aetherModelView `json:"-"`
	}
	order := g.providerOrder()
	views := make([]providerView, 0, len(manifests))
	for _, m := range manifests {
		route := g.providerRoute(m.ID)
		envs := g.pool.ListByProvider(m.ID)
		if !includeEmpty && hideEmptyVendorRow(m.ID, len(envs)) {
			continue
		}
		ready := 0
		hasKey := false
		masked := ""
		keyList := make([]aetherKeyEntry, 0, len(envs))
		chosen := ""
		bestPri := 0
		for _, e := range envs {
			if e.Disabled() {
				continue
			}
			if chosen == "" {
				chosen = e.AccountID // 还没人设为首选时，第一个启用的就是"使用中"
			}
			if p := e.Priority(); p > 0 && (bestPri == 0 || p < bestPri) {
				bestPri = p
				chosen = e.AccountID
			}
		}
		accountModels := map[string][]string{}
		accountMappings := map[string]map[string]string{}
		accountBalances := map[string]map[string]any{}
		g.settingsMu.Lock()
		for key, cfg := range g.settings.ProviderBalances {
			prefix := m.ID + "#"
			if !strings.HasPrefix(key, prefix) || cfg.URL == "" {
				continue
			}
			accountBalances[strings.TrimPrefix(key, prefix)] = map[string]any{
				"url": cfg.URL, "path": cfg.Path, "method": cfg.Method, "tokenSet": cfg.TokenEnc != "",
			}
		}
		g.settingsMu.Unlock()
		accountConcurrency := map[string]int{}
		for _, e := range envs {
			if e.Available() {
				ready++
			}
			cred := e.GetCredential("api_key")
			if cred == "" {
				cred = e.GetCredential("access_token")
			}
			if cred != "" {
				hasKey = true
				masked = maskKey(cred)
			}
			keyList = append(keyList, aetherKeyEntry{
				ID: e.AccountID, Name: e.Label, Masked: maskKey(cred),
				On: !e.Disabled(), Weight: e.Weight(), Active: e.AccountID == chosen,
				Rest: restOf(e), Quota: keyQuotaOf(e),
			})
			if ms := e.AllowedModels(); len(ms) > 0 {
				accountModels[strings.ToLower(e.AccountID)] = ms
			}
			if mm := e.ModelMapping(); len(mm) > 0 {
				accountMappings[strings.ToLower(e.AccountID)] = mm
			}
			if v, ok := e.ConcurrencyOverride(); ok {
				accountConcurrency[strings.ToLower(e.AccountID)] = v
			}
		}
		// 上游实时目录：有缓存时以它为准，静态清单只回填元数据。
		m.Models = g.catalogModels(m.ID)
		modelsSource, modelsFetched := "static", int64(0)
		listerSupported := false
		if g.catalog != nil {
			modelsSource, modelsFetched = g.catalog.source(m.ID)
			listerSupported = g.catalog.supports(m.ID)
		}
		models := make([]aetherModelView, 0, len(m.Models))
		selection, configured := g.providerModelSelection(m.ID)
		creditRates := g.providerModelCreditMap(m.ID)
		extraModels := map[string]bool{}
		for _, x := range g.providerExtraModels(m.ID) {
			extraModels[normalizeModelKey(x)] = true
		}
		for _, mm := range m.Models {
			// 前端会拼 `${provider.id}/${model.id}`：这里必须给去掉渠道前缀的
			// 模型名，否则网关页会显示 antigravity/antigravity/xxx。
			mid := strings.TrimPrefix(mm.ID, m.ID+"/")
			cw := mm.ContextWindow
			if cfg := g.lengthFor(m.ID, mid); cfg.Context > 0 {
				cw = cfg.Context
			}
			info := g.catalogInfoFor(m.ID, mid)
			rate := creditRates[mid]
			if rate == 0 {
				rate = info.Rate
			}
			models = append(models, aetherModelView{
				ID:            mid,
				Name:          mm.DisplayName,
				On:            !configured || modelInList(selection, mid),
				CreditRate:    rate,
				Max:           cw,
				Context:       cw,
				CostKnown:     info.CostKnown,
				CostIn:        info.CostIn,
				CostOut:       info.CostOut,
				CostCacheRead: info.CostCacheRead,
				Efforts:       info.Efforts,
				Free:          info.Free,
				Manual:        extraModels[normalizeModelKey(mid)],
			})
		}
		autoOff, autoOffOn := g.autoDisableOf(m.ID)
		views = append(views, providerView{
			ProviderManifest:   m,
			AccountCount:       len(envs),
			ReadyCount:         ready,
			Name:               m.DisplayName,
			Host:               hostOf(g.providerBaseURL(m.ID)),
			Key:                aetherKeyView{Set: hasKey, Masked: masked},
			KeyList:            keyList,
			AccountModels:      accountModels,
			AccountMappings:    accountMappings,
			AccountBalances:    accountBalances,
			AccountConcurrency: accountConcurrency,
			Agents:             []any{},
			Ready:              ready > 0,
			Off:                !g.providerIsEnabled(m.ID),
			AutoOff:            autoOffOn && !g.providerManuallyOff(m.ID),
			AutoOffReason:      autoOff.Reason,
			AutoOffUntil:       autoOff.Until,
			Draws:              0,
			Routing:            route.Routing,
			Sink:               route.Sink,
			Affinity:           route.Affinity,
			NoModels:           len(m.Models) == 0,
			ModelsSource:       modelsSource,
			ModelsFetched:      modelsFetched,
			ModelsMode:         g.providerModelMode(m.ID),
			ModelListSupported: listerSupported,
			ModelsView:         models,
		})
	}

	sort.SliceStable(views, func(i, j int) bool {
		oi, iok := order[views[i].ID]
		oj, jok := order[views[j].ID]
		switch {
		case iok && jok:
			return oi < oj
		case iok:
			return true
		case jok:
			return false
		}
		return false
	})

	// Models 字段名冲突（Manifest 里是模型定义），用 Map 手工拼一次
	out := make([]map[string]any, 0, len(views))
	for i, v := range views {
		proxy, accountProxies := g.providerProxyView(v.ID)
		balURL, balPath, balMethod, balTokenSet := g.providerBalanceConfigView(v.ID)
		// 接入向导要按渠道声明渲染登录方式与手动字段；不暴露这些，前端只能猜 API Key。
		var authSchema []provider.FormFieldSchema
		var webviewLogin string
		hasOAuth := false
		if p, ok := provider.Get(v.ID); ok {
			mf := p.Manifest()
			authSchema = mf.AuthFormSchema
			webviewLogin = mf.WebViewLoginURL
			hasOAuth = mf.OAuth != nil
		}
		out = append(out, map[string]any{
			"id": v.ID, "display_name": v.DisplayName, "name": v.Name,
			"family": v.Family, "region_tag": v.RegionTag,
			"brand_color": v.BrandColor, "description": v.Description,
			"network_policy": v.NetworkPolicy, "supported_auths": v.SupportedAuths,
			// 前端据此显示「写入本机 IDE」「官方授权绑定」等能力入口；
			// 这里手工拼装响应，漏掉它会让能力按钮永远不出现。
			"capabilities":     v.Capabilities,
			"auth_form_schema": authSchema, "webview_login_url": webviewLogin, "oauth": hasOAuth,
			"host": v.Host, "key": v.Key, "keyList": v.KeyList, "accountModels": v.AccountModels, "agents": v.Agents,
			"icon":                providerIconName(g.providerIcon(v.ID), v.ID),
			"accountModelMapping": views[i].AccountMappings, "accountBalances": views[i].AccountBalances, "accountConcurrency": views[i].AccountConcurrency, "maxConcurrency": g.providerConcurrencyView(v.ID), "maxRPM": g.providerRPMView(v.ID), "queueLimit": g.providerQueueLimitView(v.ID), "queueWait": g.providerQueueWaitView(v.ID), "priceRate": g.providerPriceRateView(v.ID),
			"proxy": proxy, "accountProxies": accountProxies,
			"ready": v.Ready, "off": v.Off, "draws": v.Draws, "noModels": v.NoModels,
			// 渠道编辑器的 Routing / Rate limited / Stays（保存后原样回显）。
			"routing": v.Routing, "sink": v.Sink, "affinity": v.Affinity,
			// 自定义请求头（敏感值打码回显）与额外请求体（JSON 文本）。
			"headers":      g.providerHeadersView(v.ID),
			"systemPrompt": g.providerSystemPromptView(v.ID), "concatSystem": g.providerSystemPromptConcatView(v.ID),
			"extraBody": g.providerExtraBodyView(v.ID), "fallback": g.providerFallback(v.ID), "unlisted": g.providerUnlisted(v.ID), "passUserAgent": g.providerPassUA(v.ID), "contexts": g.providerContextMap(v.ID, "context"), "outputs": g.providerContextMap(v.ID, "output"), "compacts": g.providerContextMap(v.ID, "compact"), "insecureTLS": g.providerInsecureTLS(v.ID),
			"account_count": v.AccountCount, "ready_count": v.ReadyCount,
			"models": views[i].ModelsView,
			// 模型来源：上游实时 / 静态兜底，以及能否手动刷新。
			"models_source": v.ModelsSource, "models_fetched": v.ModelsFetched,
			"models_mode": v.ModelsMode, "models_live_supported": v.ModelListSupported,
			// aether 的余额字段：URL / 表达式 / 方法，以及"令牌已保存"标记。
			// 没有渠道用量接口的（如 Qoder）也开放，用户可以自行接余额接口。
			"balanceURL": balURL, "balancePath": balPath, "balanceMethod": balMethod,
			"balanceToken": map[string]any{"takes": balanceTakesToken(v.ID, v.Capabilities.Quota), "set": balTokenSet},
			"vertex":       g.providerVertexView(v.ID),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"providers":             out,
		"tun":                   transport.DetectTUN(),
		"virtual_groups":        g.models.VirtualGroups(),
		"virtual_group_details": g.models.VirtualGroupDetails(),
		// 路由/网关页据此判断"网关是否在跑"；进程本身在服务这个请求，
		// 所以恒为 running（多实例抢占是 aether 桌面壳的概念，本项目没有）。
		"gateway": map[string]any{
			"running": true, "mine": true, "older": false, "version": "",
			"models":    len(g.models.KnownModels()),
			"url":       "http://" + g.PublicAddr(),
			"publicURL": "http://" + g.PublicAddr(),
			"lan":       g.LANEnabled(),
			"lanURLs":   g.LANURLs(),
			"calls":     []any{},
			"groups":    []any{},
		},
		// 前端按数组遍历这几项；缺了会抛 "not iterable" 并在页面底部
		// 显示红色错误条。本项目没有对应概念，给空数组而不是省略。
		"excluded":  []any{},
		"presets":   allPresets(),
		"plugins":   []any{},
		"unmoved":   []any{},
		"fileError": "",
	})
}

// providerVertexView 回显 Vertex 账号的项目/位置/凭据路径（编辑器用）。
func (g *Gateway) providerVertexView(id string) any {
	if id != "vertex" {
		return nil
	}
	for _, e := range g.pool.ListByProvider("vertex") {
		return map[string]any{
			"project":     e.GetCredential("project"),
			"location":    e.GetCredential("location"),
			"credentials": e.GetCredential("credentials_path"),
			"impersonate": "",
		}
	}
	return nil
}

// handleAccounts 返回全部账号快照，附带今日签到结果与重复账号标记。
func (g *Gateway) handleAccounts(w http.ResponseWriter, r *http.Request) {
	envs := g.pool.All()
	sort.Slice(envs, func(i, j int) bool {
		if envs[i].ProviderID != envs[j].ProviderID {
			return envs[i].ProviderID < envs[j].ProviderID
		}
		return envs[i].AccountID < envs[j].AccountID
	})

	type accountView struct {
		provider.AccountSnapshot
		DuplicateOf  string                      `json:"duplicate_of,omitempty"`
		CheckinToday map[string]scheduler.Result `json:"checkin_today,omitempty"`
		// CheckinSupported 表示该渠道实现了签到能力且声明了任务；
		// 账号页据此显示「立即签到」，不支持的渠道不出现假按钮。
		CheckinSupported bool          `json:"checkin_supported,omitempty"`
		Checkin          *quotaCheckin `json:"checkin,omitempty"`
		// QuotaError 是最近一次额度读取失败的原因，账号卡直接展示，
		// 用户不用去翻日志猜为什么没有数字。
		QuotaError string `json:"quota_error,omitempty"`
		// QuotaSupported=false 表示该渠道根本没有额度接口：页面应明确
		// 显示“不支持额度窗口”，而不是留一块加载失败的空白。
		QuotaSupported bool `json:"quota_supported"`
	}

	// 同渠道、同身份/同主密钥的账号视为重复：排序后第一个为基准，
	// 其余带 duplicate_of（local-vault 的重复账号提示）。
	seen := make(map[string]string, len(envs))
	snaps := make([]accountView, 0, len(envs))
	for _, e := range envs {
		v := accountView{
			AccountSnapshot: e.Snapshot(),
			CheckinToday:    g.checkinToday(e.AccountID),
		}
		if p, ok := provider.Get(e.ProviderID); ok {
			if m := p.Manifest(); m.Capabilities.Quota {
				v.QuotaSupported = true
			}
			if checkinSupported(p) {
				v.CheckinSupported = true
				v.Checkin = g.todayCheckinCard(e.AccountID)
			}
		}
		if msg, _ := g.quotaFailure(e.AccountID); msg != "" {
			v.QuotaError = msg
		}
		if k := accountSecretKey(e); k != "" {
			if first, ok := seen[k]; ok {
				v.DuplicateOf = first
			} else {
				base := e.Label
				if base == "" {
					base = e.AccountID
				}
				seen[k] = base
			}
		}
		snaps = append(snaps, v)
	}
	// 账号页打开时补齐缺失/过期的额度读数：新导入账号不必等 30 分钟的
	// 周期刷新，首帧空白会在几秒后的重拉里自动填上。
	g.kickQuotaReads(envs)
	writeJSON(w, http.StatusOK, map[string]any{"accounts": snaps})
}

// kickQuotaReads 后台补读缺失或过期的额度；失败后 1 分钟内不重复打上游。
func (g *Gateway) kickQuotaReads(envs []*provider.AccountEnv) {
	now := time.Now()
	for _, env := range envs {
		if env == nil || !env.Available() || !g.providerQuotaReadable(env.ProviderID) {
			continue
		}
		q := env.Snapshot().Quota
		if q != nil && q.ReadAt > 0 && now.Sub(time.Unix(q.ReadAt, 0)) < quotaFreshFor {
			continue
		}
		if msg, at := g.quotaFailure(env.AccountID); msg != "" && now.Sub(at) < time.Minute {
			continue
		}
		g.ensureQuotaRead(env)
	}
}

// accountSecretKey 返回账号的稳定身份指纹，用于重复账号检测。
// 优先用服务端身份字段（账号 ID / 邮箱），没有再退化到主密钥哈希。
func accountSecretKey(e *provider.AccountEnv) string {
	for _, k := range []string{"account_id", "email", "user_id", "chatgpt_account_id"} {
		if v := strings.TrimSpace(e.GetCredential(k)); v != "" {
			sum := sha256.Sum256([]byte(e.ProviderID + "|id|" + v))
			return hex.EncodeToString(sum[:8])
		}
	}
	for _, k := range []string{"api_key", "access_token", "refresh_token", "session_key", "token"} {
		if v := strings.TrimSpace(e.GetCredential(k)); v != "" {
			sum := sha256.Sum256([]byte(e.ProviderID + "|key|" + v))
			return hex.EncodeToString(sum[:8])
		}
	}
	return ""
}

// createAccount 创建一个账号并入池。供手动添加、OAuth 授权、本机导入三条路径复用。
func (g *Gateway) createAccount(providerID, label string, creds map[string]string, tlsProfile string) (*provider.AccountEnv, *ir.APIError) {
	p, ok := provider.Get(providerID)
	if !ok {
		return nil, ir.NewAPIError(ir.ErrCodeNoProvider, "渠道未注册: "+providerID)
	}
	m := p.Manifest()

	accountID := providerID + "-" + shortHash(label+time.Now().String())
	env := provider.NewAccountEnv(accountID, providerID)
	env.Label = label
	if env.Label == "" {
		env.Label = accountID
	}
	if creds != nil {
		env.Credentials = creds
	}

	// 指纹必须持久且稳定：同一账号永远上报同一组设备标识。
	env.Fingerprint = deriveFingerprint(accountID)
	env.NetworkPolicy = m.NetworkPolicy

	// TLS 指纹优先取显式指定，其次取渠道 spec 的声明。
	if tlsProfile != "" {
		env.TLSProfile = tlsProfile
	} else if sp := spec.Load(providerID); sp != nil && sp.TLSProfile != "" {
		env.TLSProfile = sp.TLSProfile
	}

	env.SetProxyOverride(g.resolvedAccountProxy(providerID, env.Label, accountID))
	client, err := g.clientFor(env, m.NetworkPolicy)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "创建 HTTP 客户端失败: "+err.Error())
	}
	env.SetHTTPClient(client)
	g.applyProviderTransport(env)

	g.pool.Add(env)
	return env, nil
}

// handleAddAccount 添加账号。
//
// 请求体：
//
//	{"provider_id":"mock","label":"主账号","credentials":{"token":"xxx"}}
func (g *Gateway) handleAddAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProviderID  string            `json:"provider_id"`
		Label       string            `json:"label"`
		Credentials map[string]string `json:"credentials"`
		// TLSProfile 可选：覆盖该账号的 TLS 指纹，如 "chrome_131"。
		TLSProfile string `json:"tls_profile"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if body.ProviderID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 provider_id"})
		return
	}

	env, apiErr := g.createAccount(body.ProviderID, body.Label, body.Credentials, body.TLSProfile)
	if apiErr != nil {
		writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
		return
	}

	g.saveAccountsNow()
	eventbus.Emit(eventbus.TopicAccount+".added", map[string]any{
		"provider_id": body.ProviderID,
		"account_id":  env.AccountID,
		"method":      "manual",
	})
	writeJSON(w, http.StatusOK, map[string]any{"account": env.Snapshot()})
}

// handleDeleteAccount 删除账号。
func (g *Gateway) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !g.pool.Remove(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在: " + id})
		return
	}
	g.saveAccountsNow()
	eventbus.Emit(eventbus.TopicAccount+".removed", map[string]any{"account_id": id})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "account_id": id})
}

// handleCheckIn 触发一次签到。
//
// 同一渠道的多个账号按串行执行并插入随机抖动，
// 避免"同一出口 IP 上多个账号以固定间隔打卡"这种最容易被风控识别的模式。
func (g *Gateway) handleCheckIn(w http.ResponseWriter, r *http.Request) {
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
	cap, ok := provider.AsCheckIn(p)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "该渠道不支持签到"})
		return
	}

	tasks := make([]string, 0, len(p.Manifest().Tasks))
	for _, t := range p.Manifest().Tasks {
		tasks = append(tasks, t.TaskID)
	}

	// 今天已经确定领过的不再打一次；done 在中午前可能还是厂商前一天的
	// 答案，允许再问一次。
	if prev := g.todayCheckinCard(id); prev != nil {
		settled := prev.Outcome == provider.CheckinClaimed ||
			(prev.Outcome == provider.CheckinDone && !prev.Again)
		if settled {
			writeJSON(w, http.StatusOK, map[string]any{"checkin": prev, "already": true})
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	report, err := cap.ExecuteTasks(ctx, env, tasks)
	res := checkinPressResult{User: env.Label}
	if err != nil {
		res.Outcome = provider.CheckinFailed
		res.Msg = err.Error()
		g.recordCheckin(env, res)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "checkin": g.todayCheckinCard(id)})
		return
	}
	res.Asked = true
	if report != nil {
		res.Outcome = report.Outcome
		if res.Outcome == "" {
			if report.Success {
				res.Outcome = provider.CheckinClaimed
			} else {
				res.Outcome = provider.CheckinFailed
			}
		}
		res.Credit = report.Credit
		res.Streak = report.Streak
		res.Msg = report.Message
	} else {
		res.Outcome = provider.CheckinFailed
		res.Msg = "上游没有返回签到结果"
	}
	res.Again = checkinProvisional(res.Outcome, time.Now().Unix())
	g.recordCheckin(env, res)
	writeJSON(w, http.StatusOK, map[string]any{
		"report": report, "checkin": g.todayCheckinCard(id), "outcome": res.Outcome,
	})
}

// handleRuntime 返回运行时诊断信息。
func (g *Gateway) handleRuntime(w http.ResponseWriter, r *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	writeJSON(w, http.StatusOK, map[string]any{
		"uptime_sec":    int(time.Since(g.startedAt).Seconds()),
		"go_version":    runtime.Version(),
		"goroutines":    runtime.NumGoroutine(),
		"heap_mb":       mem.HeapAlloc / 1024 / 1024,
		"state_records": g.store.Count(),
		"models":        len(g.models.KnownModels()),
		"tun":           transport.DetectTUN(),
		// 真实监听地址：桌面端 WebView 的 location.origin 是 wails.localhost，
		// 前端必须用这个值去显示"填给客户端的接入地址"。
		"public_addr": "http://" + g.PublicAddr(),
	})
}

// deriveFingerprint 由账号 ID 派生一套稳定的设备指纹。
//
// 用派生而非随机存储的原因：账号迁移/重装后指纹不会漂移，
// 否则上游会认为是"新设备登录"，触发二次验证。
func deriveFingerprint(accountID string) provider.DeviceFingerprint {
	h := func(prefix string) string {
		s := sha256.Sum256([]byte(prefix + "|" + accountID + "|aether"))
		return strings.ToUpper(hex.EncodeToString(s[:]))
	}
	machine := h("machine")
	device := h("device")

	// 生成一个看起来像真实网卡的 MAC。
	macRaw := h("mac")
	mac := strings.Join([]string{macRaw[0:2], macRaw[2:4], macRaw[4:6], macRaw[6:8], macRaw[8:10], macRaw[10:12]}, ":")

	return provider.DeviceFingerprint{
		MachineID:     machine,
		DeviceID:      device,
		MAC:           mac,
		ClientVersion: "0.0.0", // 由各渠道 spec 覆盖
		Platform:      transport.Platform(),
	}
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:10]
}

// providerIconName 是渠道条目的图标名：用户设过就用它，否则用渠道 id
// （/icons/{file} 会按品牌色生成首字母贴片）。
func providerIconName(custom, id string) string {
	if strings.TrimSpace(custom) != "" {
		return custom
	}
	return id + "-color"
}
