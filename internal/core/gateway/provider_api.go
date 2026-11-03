package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 渠道编辑器（aether 供应商页）的保存 / 显钥 / 测试端点。
//
// 内核的模型是"固定渠道 + 多账号"：渠道由代码注册，账号保存凭证。
// aether 的模型是"每个供应商一条配置，带 key 与端点"。这里做映射：
// 保存 → 写入该渠道的账号凭证（已有账号则更新），测试 → 用草稿凭证
// 真发一次最小请求，绝不拿"字段非空"冒充连通性。

// providerDraft 是编辑器提交的字段（只取我们认识的）。
type providerDraft struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Key       string       `json:"key"`
	Chat      string       `json:"chat"`
	Responses string       `json:"responses"`
	Anthropic string       `json:"anthropic"`
	Gemini    string       `json:"gemini"`
	Decide    string       `json:"decide"`
	Vertex    *vertexDraft `json:"vertex"`
	// Models 是编辑器里勾选/输入的模型（aether 的 chosen）。
	// nil = 未提交该字段（不改）；空数组 = 明确清空。
	Models []string `json:"models"`
	// ModelMode 是渠道模型目录模式：merge / fallback / allowlist；nil = 不改。
	ModelMode *string `json:"modelMode"`
	// ModelRates 是模型级积分消耗倍率（用户填写；0 或缺省表示不展示）。
	ModelRates map[string]float64 `json:"model_rates"`
	Test       []string           `json:"test"`
	// Proxy 是渠道级代理："" 跟随全局，"direct" 直连，其余是 URL。
	// null/缺省表示不改动。
	Proxy *string `json:"proxy"`
	// AccountProxies 是账号名（小写）→ 代理；未列出的跟随渠道。
	AccountProxies map[string]string `json:"accountProxies"`
	// Icon 是编辑器里选的图标（"file:<name>"）；nil=不改。
	Icon *string `json:"icon"`
	// Contexts / Outputs / Compacts 是每模型长度（高级）；nil = 不改，空 map = 清除。
	Contexts map[string]int `json:"contexts"`
	Outputs  map[string]int `json:"outputs"`
	Compacts map[string]int `json:"compacts"`
	// PassUserAgent 是"把 Agent 的 UA 透传给上游"开关（高级）；nil = 不改。
	PassUserAgent *bool `json:"passUserAgent"`
	// InsecureTLS 是"不校验证书"开关（高级 → Don't check its certificate）；
	// nil = 不改。
	InsecureTLS *bool `json:"insecureTLS"`
	// Unlisted 是"只在路由分组里使用"开关（高级 → Only through routing groups）；
	// nil = 不改。
	Unlisted *bool `json:"unlisted"`
	// Fallback 是渠道级兜底模型链（高级 → Fallback，形如 "provider/model"）；
	// nil = 不改，空列表 = 清除。
	Fallback []string `json:"fallback"`
	// PriceRate 是渠道计费倍率（高级 → Price rate，0.8 或 1.5 之类）；
	// nil = 不改，1 = 回到官方价。
	PriceRate *float64 `json:"priceRate"`
	// MaxConcurrency / MaxRPM 是渠道级"每个账号"的上限（高级 → Concurrency /
	// Requests per minute）；nil = 不改，等于全局值时清除覆盖（跟随全局）。
	MaxConcurrency *int `json:"maxConcurrency"`
	MaxRPM         *int `json:"maxRPM"`
	// QueueLimit / QueueWait 是渠道级队列长度与等待秒数（0 = 跟随全局）。
	QueueLimit *int `json:"queueLimit"`
	QueueWait  *int `json:"queueWait"`
	// Headers 是渠道级自定义请求头（编辑器 高级 → 请求头）；nil = 不改。
	Headers map[string]string `json:"headers"`
	// ExtraBody 是渠道级额外请求体字段（编辑器 高级 → 额外请求体）；nil = 不改。
	ExtraBody json.RawMessage `json:"extraBody"`
	// SystemPrompt 是渠道级默认系统提示词；nil = 不改，空串 = 清除。
	SystemPrompt *string `json:"systemPrompt"`
	// ConcatSystem 控制渠道提示词是否与用户提示词连接。
	ConcatSystem *bool `json:"concatSystem"`
	// Routing / Sink / Affinity 是渠道编辑器的"请求怎么分"三项：
	// 账号选择方式、限流后是否排到队尾、会话粘性档位。nil = 不改。
	Routing  *string `json:"routing"`
	Sink     *bool   `json:"sink"`
	Affinity *string `json:"affinity"`
	// BalanceURL / BalancePath / BalanceMethod 是自定义余额接口（aether 同名）。
	BalanceURL    string `json:"balanceURL"`
	BalancePath   string `json:"balancePath"`
	BalanceMethod string `json:"balanceMethod"`
	BalanceToken  string `json:"balanceToken"`
	ClearBalance  bool   `json:"clearBalanceToken"`
}

// cleanProviderModels 去空白、去重，保留编辑器提交的模型 ID 形态
// （全名或上游名），ServesModel 两种都认。
func cleanProviderModels(models []string) []string {
	out := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

func decodeDraft(r *http.Request) (*providerDraft, error) {
	var d providerDraft
	err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&d)
	return &d, err
}

// trimEndpoint 把编辑器里的完整端点地址折成渠道需要的 base_url。
func trimEndpoint(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	for _, suffix := range []string{"/chat/completions", "/responses", "/v1/messages", "/v1/systemone"} {
		raw = strings.TrimSuffix(raw, suffix)
	}
	return strings.TrimRight(raw, "/")
}

// customCoreProvider 把「自定义供应商」草稿落到内核已有协议渠道：
// OpenAI Chat → openai-compat，Responses → codex，Anthropic → claude-sub。
// Gemini 上游尚未实现，明确拒绝而不是伪装成 Chat。
func customCoreProvider(d *providerDraft) (string, map[string]string, string) {
	creds := draftCredentials(d)
	key := strings.TrimSpace(d.Key)
	switch {
	case trimEndpoint(d.Chat) != "":
		return "openai-compat", creds, ""
	case trimEndpoint(d.Responses) != "":
		if key != "" {
			creds["access_token"] = key
			delete(creds, "api_key")
		}
		return "codex", creds, ""
	case trimEndpoint(d.Anthropic) != "":
		if key != "" {
			creds["access_token"] = key
			delete(creds, "api_key")
		}
		return "claude-sub", creds, ""
	case trimEndpoint(d.Gemini) != "":
		creds["base_url"] = trimEndpoint(d.Gemini)
		return "gemini-compat", creds, ""
	case trimEndpoint(d.Decide) != "":
		creds["base_url"] = trimEndpoint(d.Decide)
		return "typesafe", creds, ""
	default:
		return "", nil, "自定义供应商需要至少一个 Base URL（Chat / Responses / Anthropic）"
	}
}

// draftCredentials 把草稿映射成账号凭证。不同渠道的凭证键不同：
// Claude 订阅用 access_token，其余用 api_key。
func draftCredentials(d *providerDraft) map[string]string {
	creds := map[string]string{}
	key := strings.TrimSpace(d.Key)
	base := trimEndpoint(d.Chat)
	if base == "" {
		base = trimEndpoint(d.Responses)
	}
	if base == "" {
		base = trimEndpoint(d.Anthropic)
	}
	if base == "" {
		base = trimEndpoint(d.Decide)
	}
	if key != "" {
		if strings.HasPrefix(key, "sk-ant-oat") {
			creds["access_token"] = key
		} else {
			creds["api_key"] = key
		}
	}
	if base != "" {
		creds["base_url"] = base
	}
	return creds
}

// handleProviderSave POST /api/provider/save
//
// 已有账号：合并凭证（不覆盖未提交的字段）；没有：新建一个账号。
// 返回完整的渠道列表，前端据此重绘。
func (g *Gateway) handleProviderSave(w http.ResponseWriter, r *http.Request) {
	d, err := decodeDraft(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	creds := draftCredentials(d)
	if d.ID == "vertex" {
		vc, problem := vertexCredentials(d)
		if problem != "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": problem})
			return
		}
		creds = vc
	}
	if d.BalanceURL != "" || d.BalanceToken != "" || d.ClearBalance {
		g.settingsMu.Lock()
		err := g.saveProviderBalanceLocked(d.ID, ProviderBalanceConfig{
			URL: d.BalanceURL, Path: d.BalancePath, Method: d.BalanceMethod,
		}, d.BalanceToken, d.ClearBalance)
		g.settingsMu.Unlock()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存余额配置失败: " + err.Error()})
			return
		}
	}
	if _, known := provider.Get(d.ID); known && d.ModelMode != nil {
		// 目录模式先落盘并重建路由表：切换 allowlist/fallback 后模型解析
		// 必须立即按新模式生效，不能等下一次刷新。
		if err := g.setProviderModelMode(d.ID, *d.ModelMode); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		g.models.Rebuild()
		if g.catalog != nil && g.catalog.supports(d.ID) {
			go g.catalog.maybeRefresh(d.ID)
		}
	}
	if _, known := provider.Get(d.ID); known && d.Models != nil {
		// 内置渠道的模型选择落 settings（渠道级）；账号级白名单仍由账号设置维护。
		if err := g.saveProviderModelSelection(d.ID, d.Models, d.ModelRates); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存模型选择失败: " + err.Error()})
			return
		}
	}
	if _, ok := provider.Get(d.ID); !ok {
		// 编辑器里的「自定义供应商」：id 是前端按名字生成的 slug，
		// 内核没有这个渠道。按所选协议落到已有渠道的独立账号上，
		// 这样 base_url + key 立刻可用，也不必动态注册渠道。
		coreID, coreCreds, problem := customCoreProvider(d)
		if coreID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": problem})
			return
		}
		label := strings.TrimSpace(d.Name)
		if label == "" {
			label = d.ID
		}
		created, apiErr := g.createAccount(coreID, label, coreCreds, "")
		if apiErr != nil {
			writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
			return
		}
		if models := cleanProviderModels(d.Models); len(models) > 0 {
			created.SetAllowedModels(models)
		}
		g.saveAccountsNow()
		if d.Icon != nil {
			// 自定义供应商在核心里是某个协议渠道的账号，图标挂在那个渠道上。
			g.settingsMu.Lock()
			g.setProviderIconLocked(coreID, *d.Icon)
			_ = g.saveSettings()
			g.settingsMu.Unlock()
		}
		g.respondProviders(w, r)
		return
	}

	envs := g.pool.ListByProvider(d.ID)
	var env *provider.AccountEnv
	if len(envs) > 0 {
		env = envs[0]
	} else {
		if d.ID == "vertex" && strings.TrimSpace(creds["adc_json"]) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Vertex 首次添加需要有效的 ADC 凭据文件；可先运行 gcloud auth application-default login，或填写凭据文件路径"})
			return
		}
		created, apiErr := g.createAccount(d.ID, d.Name, creds, "")
		if apiErr != nil {
			writeJSON(w, apiErr.StatusCode(), map[string]any{"error": apiErr.Message})
			return
		}
		env = created
	}
	for k, v := range creds {
		if v != "" {
			env.SetCredential(k, v)
		}
	}
	if env.Label == "" || (d.Name != "" && env.Label == env.AccountID) {
		env.Label = d.Name
	}
	if d.Proxy != nil || d.AccountProxies != nil {
		if err := g.setProviderProxy(d.ID, d.Proxy, d.AccountProxies); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存代理设置失败: " + err.Error()})
			return
		}
	}
	if d.Icon != nil {
		g.settingsMu.Lock()
		g.setProviderIconLocked(d.ID, *d.Icon)
		_ = g.saveSettings()
		g.settingsMu.Unlock()
	}
	if d.Routing != nil || d.Sink != nil || d.Affinity != nil {
		if err := g.setProviderRoute(d.ID, d.Routing, d.Sink, d.Affinity); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	if d.Headers != nil {
		if err := g.setProviderHeaders(d.ID, d.Headers); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		// 已存在的账号要把新的头立刻套上（不用重启）。
		for _, env := range g.pool.ListByProvider(d.ID) {
			g.applyProviderTransport(env)
		}
	}
	if d.Contexts != nil || d.Outputs != nil || d.Compacts != nil {
		if err := g.setProviderLengths(d.ID, d.Contexts, d.Outputs, d.Compacts); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "保存模型长度失败: " + err.Error()})
			return
		}
	}
	if d.PassUserAgent != nil {
		if err := g.setProviderPassUA(d.ID, *d.PassUserAgent); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存 UA 透传开关失败: " + err.Error()})
			return
		}
		for _, env := range g.pool.ListByProvider(d.ID) {
			g.applyProviderTransport(env)
		}
	}
	if d.InsecureTLS != nil {
		if err := g.setProviderInsecureTLS(d.ID, *d.InsecureTLS); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存 TLS 开关失败: " + err.Error()})
			return
		}
		// 立刻按新开关重建该渠道的客户端（不用重启）。
		for _, env := range g.pool.ListByProvider(d.ID) {
			if err := g.rebuildAccountClient(env); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "重建客户端失败: " + err.Error()})
				return
			}
		}
	}
	if d.Unlisted != nil {
		if err := g.setProviderUnlisted(d.ID, *d.Unlisted); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存 unlisted 失败: " + err.Error()})
			return
		}
	}
	if d.Fallback != nil {
		if err := g.setProviderFallback(d.ID, d.Fallback); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存兜底链失败: " + err.Error()})
			return
		}
	}
	if d.PriceRate != nil {
		if err := g.setProviderPriceRate(d.ID, d.PriceRate); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	if d.MaxConcurrency != nil || d.MaxRPM != nil || d.QueueLimit != nil || d.QueueWait != nil {
		if err := g.setProviderLimits(d.ID, d.MaxConcurrency, d.MaxRPM, d.QueueLimit, d.QueueWait); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存渠道上限失败: " + err.Error()})
			return
		}
	}
	if len(d.ExtraBody) > 0 {
		if err := g.setProviderExtraBody(d.ID, d.ExtraBody); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		for _, env := range g.pool.ListByProvider(d.ID) {
			g.applyProviderTransport(env)
		}
	}
	if d.SystemPrompt != nil || d.ConcatSystem != nil {
		if err := g.setProviderSystemPrompt(d.ID, d.SystemPrompt, d.ConcatSystem); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "保存系统提示词失败: " + err.Error()})
			return
		}
		for _, env := range g.pool.ListByProvider(d.ID) {
			g.applyProviderTransport(env)
		}
	}
	g.saveAccountsNow()
	g.respondProviders(w, r)
}

// handleProviderKey GET/POST /api/provider/key —— 显示已保存的密钥。
func (g *Gateway) handleProviderKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Account string `json:"account"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	envs := g.pool.ListByProvider(body.ID)
	for _, e := range envs {
		if body.Account != "" && e.AccountID != body.Account {
			continue
		}
		key := e.GetCredential("api_key")
		if key == "" {
			key = e.GetCredential("access_token")
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"key": ""})
}

// handleProviderTest POST /api/provider/test
//
// 用草稿里的 key/端点新建一个临时账号，对每个待测模型发一次最小流式请求。
// 任何一次失败都如实报告；返回顺序与 test 数组一致。
func (g *Gateway) handleProviderTest(w http.ResponseWriter, r *http.Request) {
	d, err := decodeDraft(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	p, ok := provider.Get(d.ID)
	probeID := d.ID
	if !ok {
		// 自定义供应商（slug 未注册）：按所选协议映射到内核渠道再探测。
		coreID, _, problem := customCoreProvider(d)
		if coreID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": problem})
			return
		}
		p, ok = provider.Get(coreID)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "渠道未注册: " + coreID})
			return
		}
		probeID = coreID
	}
	chat, ok := provider.AsChat(p)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "该渠道不支持对话测试"})
		return
	}
	m := p.Manifest()
	models := d.Test
	creds := draftCredentials(d)
	if probeID != d.ID {
		_, creds, _ = customCoreProvider(d)
	}
	client, err := g.tr.NewClient(m.NetworkPolicy, "", "")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	newEnv := func() *provider.AccountEnv {
		env := provider.NewAccountEnv("probe-"+probeID, probeID)
		env.Credentials = creds
		env.SetHTTPClient(client)
		env.NetworkPolicy = m.NetworkPolicy
		return env
	}
	if len(models) == 0 {
		probeModels := g.probeOrder(probeID, m.Models)
		if len(probeModels) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"results": []map[string]any{{
				"protocol": "chat", "model": "", "ok": false, "error": "渠道没有登记模型，无法探测",
			}}})
			return
		}
		// 按成本从低到高试，最多 3 个：最便宜的失败后升级到次便宜。
		model, ok, msg, ms := probeFirstOK(r.Context(), chat, newEnv(), probeModels, 3)
		writeJSON(w, http.StatusOK, map[string]any{"results": []map[string]any{{"protocol": "chat", "model": model.ID, "ok": ok, "error": msg, "ms": ms}}})
		return
	}
	results := make([]map[string]any, 0, len(models))
	for _, id := range models {
		upstream := strings.TrimPrefix(id, d.ID+"/")
		if probeID != d.ID {
			upstream = strings.TrimPrefix(upstream, probeID+"/")
		}
		env := newEnv()

		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		ok, msg := probeModel(ctx, chat, env, upstream)
		cancel()
		results = append(results, map[string]any{"protocol": "chat", "model": id, "ok": ok, "error": msg})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// probeModel 真发一次最小请求：收到任何非错误事件即视为连通。
func probeModel(ctx context.Context, chat provider.ChatCapability, env *provider.AccountEnv, upstream string) (bool, string) {
	req := &ir.UnifiedChatRequest{
		Model:              upstream,
		DownstreamProtocol: ir.ProtocolOpenAIChat,
		UpstreamModel:      upstream,
		ProviderID:         env.ProviderID,
		Items:              []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "ping"}},
		Stream:             true,
	}
	ch, err := chat.ChatStream(ctx, env, req)
	if err != nil {
		return false, err.Error()
	}
	for {
		select {
		case <-ctx.Done():
			return false, "超时：25 秒内没有回应"
		case ev, ok := <-ch:
			if !ok {
				return true, ""
			}
			switch ev.Type {
			case ir.EventError:
				if ev.Err != nil {
					return false, ev.Err.Error()
				}
				return false, "上游返回错误"
			case ir.EventTextDelta, ir.EventReasoningDelta, ir.EventToolCallStart, ir.EventUsage, ir.EventDone:
				return true, ""
			}
		}
	}
}

// respondProviders 把 handleProviders 的结果写回当前响应。
func (g *Gateway) respondProviders(w http.ResponseWriter, r *http.Request) {
	rec := &discardResponse{header: http.Header{}}
	g.handleProviders(rec, r)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	w.WriteHeader(rec.status)
	_, _ = w.Write(rec.body)
}

// saveAccountsNow 立即落盘账号（保存密钥后不该等 10 秒自动保存）。
func (g *Gateway) saveAccountsNow() {
	if g.pool != nil {
		_ = g.pool.SaveAccounts()
	}
}

// aetherKeyEntry 是渠道编辑页「Accounts / Keys」列表的一行。
// 内核的账号映射成一行 key：启用状态、名称与掩码后的凭证。
type aetherKeyEntry struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Masked   string `json:"masked,omitempty"`
	On       bool   `json:"on"`
	Active   bool   `json:"active,omitempty"`
	Weight   int    `json:"weight,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	// Rest 表示这个账号正被网关跳过（429 冷却 / 额度耗尽），
	// 前端在密钥行下显示"Resting · why · back at"，并可 "Try now"。
	Rest *aetherKeyRest `json:"rest,omitempty"`
	// Quota 是该账号此刻的额度读数（渠道编辑器账号行的小徽标用，
	// custom-quota.js 读取；没有可展示内容时缺省）。
	Quota *keyQuotaView `json:"quota,omitempty"`
}

// aetherKeyRest 是账号的"休息"信息，字段名对齐 aether 前端。
type aetherKeyRest struct {
	Key    string `json:"key"`
	Why    string `json:"why"`
	Status string `json:"status,omitempty"`
	// Until Unix 毫秒（前端 new Date(until)）。
	Until int64 `json:"until"`
}

// handleKeysAction POST /api/keys/{action}
//
// 动作对齐 aether 编辑器：on / off / rename / remove / add / replace / protocol。
func (g *Gateway) handleKeysAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var body struct {
		ID       string `json:"id"`
		Ref      string `json:"ref"`
		Name     string `json:"name"`
		Key      string `json:"key"`
		Protocol string `json:"protocol"`
		Weight   int    `json:"weight"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if body.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少渠道 id"})
		return
	}
	fail := func(status int, msg string) {
		writeJSON(w, status, map[string]any{"error": msg})
	}
	find := func() *provider.AccountEnv {
		for _, e := range g.pool.ListByProvider(body.ID) {
			if e.AccountID == body.Ref {
				return e
			}
		}
		return nil
	}

	switch action {
	case "add":
		key := strings.TrimSpace(body.Key)
		if key == "" {
			fail(http.StatusBadRequest, "密钥不能为空")
			return
		}
		creds := map[string]string{}
		if strings.HasPrefix(key, "sk-ant-oat") {
			creds["access_token"] = key
		} else {
			creds["api_key"] = key
		}
		if _, apiErr := g.createAccount(body.ID, body.Name, creds, ""); apiErr != nil {
			fail(apiErr.StatusCode(), apiErr.Message)
			return
		}
	case "on", "off":
		env := find()
		if env == nil {
			fail(http.StatusNotFound, "账号不存在: "+body.Ref)
			return
		}
		env.SetDisabled(action == "off")
	case "rename":
		env := find()
		if env == nil {
			fail(http.StatusNotFound, "账号不存在: "+body.Ref)
			return
		}
		env.Label = strings.TrimSpace(body.Name)
		if env.Label == "" {
			env.Label = env.AccountID
		}
	case "replace":
		env := find()
		if env == nil {
			fail(http.StatusNotFound, "账号不存在: "+body.Ref)
			return
		}
		key := strings.TrimSpace(body.Key)
		if key == "" {
			fail(http.StatusBadRequest, "密钥不能为空")
			return
		}
		if strings.HasPrefix(key, "sk-ant-oat") {
			env.SetCredential("access_token", key)
		} else {
			env.SetCredential("api_key", key)
		}
	case "remove":
		env := find()
		if env == nil {
			fail(http.StatusNotFound, "账号不存在: "+body.Ref)
			return
		}
		g.pool.Remove(env.AccountID)
	case "use":
		env := find()
		if env == nil {
			fail(http.StatusNotFound, "账号不存在: "+body.Ref)
			return
		}
		// 显式顺序：目标设为 1，其余已排序的顺延。
		for _, e := range g.pool.ListByProvider(body.ID) {
			if p := e.Priority(); p > 0 {
				e.SetPriority(p + 1)
			}
		}
		env.SetPriority(1)
	case "weight":
		env := find()
		if env == nil {
			fail(http.StatusNotFound, "账号不存在: "+body.Ref)
			return
		}
		env.SetWeight(body.Weight)
	case "protocol":
		// 本项目的渠道协议由 Manifest/spec 决定；接受该动作但不改变路由。
	default:
		fail(http.StatusNotFound, "未知动作: "+action)
		return
	}
	g.saveAccountsNow()
	g.respondProviders(w, r)
}

// handleProviderAccountMapping POST /api/provider/accountmapping
//
// 账号级模型映射（relay-panel 的 model_mapping）：客户端/路由模型名 → 该账号
// 真实的上游模型名。支持精确名与 * 通配；提交空对象表示清除。
func (g *Gateway) handleProviderAccountMapping(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string            `json:"id"`
		Account string            `json:"account"`
		Mapping map[string]string `json:"mapping"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	for _, e := range g.pool.ListByProvider(body.ID) {
		if e.AccountID == body.Account {
			e.SetModelMapping(body.Mapping)
			g.saveAccountsNow()
			g.respondProviders(w, r)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在: " + body.Account})
}

// handleProviderAccountModels POST /api/provider/accountmodels
//
// 账号级模型白名单：allow 为空表示恢复"全部模型"。
func (g *Gateway) handleProviderAccountModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string   `json:"id"`
		Account string   `json:"account"`
		Allow   []string `json:"allow"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	for _, e := range g.pool.ListByProvider(body.ID) {
		if e.AccountID == body.Account {
			e.SetAllowedModels(body.Allow)
			g.saveAccountsNow()
			g.respondProviders(w, r)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "账号不存在: " + body.Account})
}
