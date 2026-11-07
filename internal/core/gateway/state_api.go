package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"aether-cockpit/internal/core/clients"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// 本文件提供 aether 风格前端的兼容 API。
//
// 前端是直接基于 aether 的 app.js 改造而来（去品牌化 + 换色板），
// 它期望 POST /api/state 一次拿到全部状态，其余动作走各自的 path。
// 这里做的是"数据形状适配"：把我们的 clients / providers 映射成它认识的结构，
// 前端渲染逻辑一行不改，视觉与交互自然与上游一致。

// aetherOption 是模型选择器里的一个选项。
type aetherOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	Icon  string `json:"icon,omitempty"`
	Group string `json:"group,omitempty"`
	// Ref 标记"这是 Aether Cockpit 提供的模型/路由组"：Agents 页用它决定
	// 是否显示接入开关，并把选中的值写进客户端配置。
	Ref string `json:"ref,omitempty"`
}

// aetherField 是 Agent 行上的一个控件（模型、思考强度等）。
type aetherField struct {
	Key     string         `json:"key"`
	Label   string         `json:"label"`
	Value   string         `json:"value"`
	Options []aetherOption `json:"options"`
}

// aetherAgent 是 Agent 列表的一行。
type aetherAgent struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Icon   string        `json:"icon"`
	Path   string        `json:"path"`
	Fields []aetherField `json:"fields"`
	Wired  bool          `json:"wired,omitempty"`
	// Detected 表示本机确实装了这个客户端（配置文件存在，或可执行文件在 PATH 里）。
	// 前端据此把"没装也没接"的条目收起来——十几条全列会让真正在用的那几个
	// 淹在一堆"未检测到"里。仍然可以手动展开看全部。
	Detected bool           `json:"detected,omitempty"`
	Source   string         `json:"source,omitempty"`
	Models   map[string]any `json:"models,omitempty"`
}

// aetherProfile 是配置档。
type aetherProfile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
}

// aetherState 是 POST /api/state 的响应。
type aetherState struct {
	Agents   []aetherAgent   `json:"agents"`
	Clients  []any           `json:"clients"`
	Profiles []aetherProfile `json:"profiles"`
	Catalog  string          `json:"catalog"`
	Settings map[string]any  `json:"settings"`
	FX       map[string]any  `json:"fx"`
	Served   int             `json:"served"`
	// Connected 是连接动作的结果（前端读取 how）；Notice 是附加提醒。
	Connected map[string]any `json:"connected,omitempty"`
	Notice    string         `json:"notice,omitempty"`
}

// handleAetherState 聚合返回前端需要的全部状态。
func (g *Gateway) handleAetherState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.buildAetherState())
}

// agentFields 返回某个 Agent 在 Agents 页要展示的控件。
//
// 之前每个客户端只有 model 一个字段：Claude Code 的 opus/sonnet/haiku 被压成
// 同一个模型，Codex 也没有推理档位。这里按客户端补上真实存在的字段——
// 只补确定支持的，不为了"字段多"硬塞。
func (g *Gateway) agentFields(id, model string, opts []aetherOption) []aetherField {
	fields := []aetherField{{
		Key:     "model",
		Label:   "model",
		Value:   model,
		Options: opts,
	}}
	staged := g.agentStageOf(id)
	pick := func(key, label, cur string) aetherField {
		if v := strings.TrimSpace(staged[key]); v != "" {
			cur = v
		}
		return aetherField{Key: key, Label: label, Value: cur, Options: opts}
	}
	switch id {
	case "claude_code":
		// 档位模型：分别给 opus/sonnet/haiku 选，空表示跟随主模型。
		fields = append(fields,
			pick("opus", "opus 档", ""),
			pick("sonnet", "sonnet 档", ""),
			pick("haiku", "haiku 档", ""))
	case "codex_cli":
		cur := strings.TrimSpace(staged["effort"])
		fields = append(fields, aetherField{
			Key: "effort", Label: "推理档位", Value: cur,
			Options: []aetherOption{
				{Value: "", Label: "跟随 Codex 默认"},
				{Value: "low", Label: "low"},
				{Value: "medium", Label: "medium"},
				{Value: "high", Label: "high"},
				{Value: "xhigh", Label: "xhigh"},
			},
		})
	}
	return fields
}

// agentStageOf 返回某个 Agent 暂存的字段值（用户在页面上选过、尚未应用）。
func (g *Gateway) agentStageOf(id string) map[string]string {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	all, _ := g.settings.UI["agentStage"].(map[string]any)
	one, _ := all[id].(map[string]any)
	out := make(map[string]string, len(one))
	for k, v := range one {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func (g *Gateway) buildAetherState() aetherState {
	opts := g.modelOptions()

	statuses := g.clientsManager().List()
	agents := make([]aetherAgent, 0, len(statuses))
	for _, st := range statuses {
		model := ""
		wired := false
		if st.Managed != nil {
			model = st.Managed.Model
			wired = st.Managed.BaseURL != ""
		}
		hidden := g.agentModelPrefs(st.ID)["hidden"]
		hiddenSet := stringSet(hidden)
		shown := 0
		for _, o := range opts {
			if !hiddenSet[o.Value] {
				shown++
			}
		}
		agents = append(agents, aetherAgent{
			ID:       st.ID,
			Name:     st.Name,
			Icon:     st.ID + "-color",
			Path:     st.Path,
			Fields:   g.agentFields(st.ID, model, opts),
			Wired:    wired,
			Detected: st.Detected,
			Models:   map[string]any{"shown": shown, "listed": len(opts)},
		})
	}

	profiles := make([]aetherProfile, 0, 4)
	g.settingsMu.Lock()
	profSnapshot := append([]Profile(nil), g.settings.Profiles...)
	g.settingsMu.Unlock()
	for _, p := range profSnapshot {
		profiles = append(profiles, aetherProfile{ID: p.ID, Name: p.Name, UpdatedAt: p.UpdatedAt})
	}

	served := 0
	for _, m := range provider.AllManifests() {
		served += len(m.Models)
	}

	connected := map[string]any{}
	for _, a := range agents {
		if a.Wired {
			connected[a.ID] = true
		}
	}
	st := aetherState{
		Agents:   agents,
		Clients:  []any{},
		Profiles: profiles,
		Catalog:  "",
		Settings: g.aetherSettings(),
		FX:       map[string]any{"rate": 7.1, "stale": true},
		Served:   served,
	}
	if len(connected) > 0 {
		st.Connected = map[string]any{"how": "joined", "agents": connected}
	}
	return st
}

// aetherSettings 是 /api/state 里的 UI 偏好：默认值 + 已保存的透传偏好。
// 前端的 applyPrefs 用 state.settings 应用主题/语言等，缺了它们刷新后
// 会回到默认值（设置页保存过的偏好看起来像没生效）。
func (g *Gateway) aetherSettings() map[string]any {
	out := map[string]any{"theme": "light", "lang": "zh", "currency": "cny"}
	g.settingsMu.Lock()
	for k, v := range g.settings.UI {
		out[k] = v
	}
	g.settingsMu.Unlock()
	// 脱敏页从 /api/state 读 prefs.redact 决定卡片状态；以引擎实际状态为准。
	eng := g.redactEngine()
	if _, ok := out["redact"]; !ok {
		out["redact"] = eng.Enabled()
	}
	if _, ok := out["redactActions"]; !ok {
		if a := eng.Actions(); len(a) > 0 {
			out["redactActions"] = a
		}
	}
	if _, ok := out["redactPersonal"]; !ok {
		out["redactPersonal"] = eng.Personal()
	}
	out["redactRules"] = customRulesView(eng)
	out["redactAllow"] = eng.Allow()
	out["dir"] = g.configDir()
	out["portable"] = false
	return out
}

// modelOptions 把所有渠道的模型摊平成前端认识的选项列表。
func (g *Gateway) modelOptions() []aetherOption {
	out := make([]aetherOption, 0, 128)

	// 逻辑模型：重定向优先于同名路由组（与 Resolve 的顺序一致）。
	redirectNames := g.redirectNames()
	redirectSet := make(map[string]bool, len(redirectNames))
	for _, name := range redirectNames {
		redirectSet[name] = true
	}
	for _, name := range g.modelGroupNames() {
		if redirectSet[name] {
			continue
		}
		out = append(out, aetherOption{
			Value: name,
			Label: name,
			Note:  "路由组",
			Group: "路由组",
			Ref:   name,
		})
	}
	for _, name := range redirectNames {
		out = append(out, aetherOption{
			Value: name,
			Label: name,
			Note:  "模型重定向",
			Group: "模型重定向",
			Ref:   name,
		})
	}

	seen := make(map[string]bool, 128)
	for _, m := range provider.AllManifests() {
		if !g.providerIsEnabled(m.ID) {
			continue
		}
		// 走渠道的有效目录（含 merge/fallback/allowlist），否则 Agent 页
		// 会看到与 /v1/models、渠道页不一致的模型清单。
		for _, model := range g.catalogModels(m.ID) {
			up := strings.TrimSpace(model.UpstreamModel)
			if up == "" {
				up = strings.TrimPrefix(model.ID, m.ID+"/")
			}
			val := m.ID + "/" + up
			if seen[val] {
				continue
			}
			seen[val] = true

			label := model.DisplayName
			if label == "" {
				label = up
			}
			out = append(out, aetherOption{
				Value: val,
				Label: label,
				Note:  m.DisplayName,
				Group: m.ID,
				Ref:   val,
			})
		}
	}
	return out
}

// modelGroupNames 返回已配置的虚拟模型组名。
func (g *Gateway) modelGroupNames() []string {
	groups := g.models.VirtualGroups()
	out := make([]string, 0, len(groups))
	for name := range groups {
		out = append(out, name)
	}
	return out
}

// handleAetherSync POST /api/sync —— Agents 页"刷新模型清单"的收尾：
// 返回最新 state（前端拿到后重绘 Agents 与渠道模型）。
func (g *Gateway) handleAetherSync(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.buildAetherState())
}

// handleAetherSet 处理前端的"设置某个字段"动作。
//
//	POST /api/set  {"agent":"claude-code","field":"model","value":"openai-compat/xxx"}
func (g *Gateway) handleAetherSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent string `json:"agent"`
		Field string `json:"field"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if body.Agent == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 agent"})
		return
	}

	baseURL := "http://" + g.publicAddr
	key := g.GatewayKey()
	if key == "" {
		key = "aether"
	}

	if _, err := g.clientsManager().Apply(body.Agent, clients.ApplyParams{
		BaseURL: baseURL,
		APIKey:  key,
		Model:   body.Value,
	}); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, g.buildAetherState())
}

/* ---------------- 渠道视图的辅助类型 ---------------- */

// aetherModelView 是渠道行里一个模型的展示形态。
type aetherModelView struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// On 表示该模型对下游可见（渠道级模型选择；未配置时默认全部开放）。
	On bool `json:"on"`
	// CreditRate 是模型级积分消耗倍率（0 表示未配置）。
	CreditRate float64 `json:"credit_rate,omitempty"`
	// Max / Context 是上下文窗口：渠道级覆盖值优先，否则渠道声明值。
	// 编辑器用它做"每个模型的最大窗口"一键选择。
	Max     int `json:"max,omitempty"`
	Context int `json:"context,omitempty"`
	// CostKnown / CostIn / CostOut 来自 models.dev（美元 / 百万 tokens）。
	CostKnown     bool    `json:"cost_known,omitempty"`
	CostIn        float64 `json:"cost_in,omitempty"`
	CostOut       float64 `json:"cost_out,omitempty"`
	CostCacheRead float64 `json:"cost_cache_read,omitempty"`
	// Efforts 是模型支持的推理档位（low/high/max…）。
	Efforts []string `json:"efforts,omitempty"`
	// Free 表示上游目录明确标了免费（Qoder 的 is_free）。
	Free bool `json:"free,omitempty"`
	// Manual 表示这是用户手动补进目录的模型（上游 /models 漏报）。
	Manual bool `json:"manual,omitempty"`
}

// aetherKeyView 是 API Key 的展示形态：只给是否设置与掩码后的样子。
type aetherKeyView struct {
	Set    bool   `json:"set"`
	Masked string `json:"masked,omitempty"`
}

// hostOf 从 URL 里取主机名，用于渠道行的副标题。
func hostOf(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host
}

// maskKey 保留首尾各 4 位，中间省略。
//
// 这不是安全措施（安全靠加密存储），只是让用户能认出是哪把 key，
// 同时避免明文出现在屏幕共享或截图里。
func maskKey(k string) string {
	if len(k) <= 10 {
		return strings.Repeat("•", len(k))
	}
	return k[:4] + "…" + k[len(k)-4:]
}

// providerBaseURL 取渠道的上游地址：优先 spec，其次 Manifest 的端点声明。
func (g *Gateway) providerBaseURL(id string) string {
	if sp := spec.Load(id); sp != nil && sp.BaseURL != "" {
		return sp.BaseURL
	}
	return ""
}
