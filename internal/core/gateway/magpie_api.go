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

// 本文件提供 magpie 风格前端的兼容 API。
//
// 前端是直接基于 magpie 的 app.js 改造而来（去品牌化 + 换色板），
// 它期望 POST /api/state 一次拿到全部状态，其余动作走各自的 path。
// 这里做的是"数据形状适配"：把我们的 clients / providers 映射成它认识的结构，
// 前端渲染逻辑一行不改，视觉与交互自然与上游一致。

// magpieOption 是模型选择器里的一个选项。
type magpieOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
	Icon  string `json:"icon,omitempty"`
	Group string `json:"group,omitempty"`
	// Ref 标记"这是 Aether Cockpit 提供的模型/路由组"：Agents 页用它决定
	// 是否显示接入开关，并把选中的值写进客户端配置。
	Ref string `json:"ref,omitempty"`
}

// magpieField 是 Agent 行上的一个控件（模型、思考强度等）。
type magpieField struct {
	Key     string         `json:"key"`
	Label   string         `json:"label"`
	Value   string         `json:"value"`
	Options []magpieOption `json:"options"`
}

// magpieAgent 是 Agent 列表的一行。
type magpieAgent struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Icon   string         `json:"icon"`
	Path   string         `json:"path"`
	Fields []magpieField  `json:"fields"`
	Wired  bool           `json:"wired,omitempty"`
	Source string         `json:"source,omitempty"`
	Models map[string]any `json:"models,omitempty"`
}

// magpieProfile 是配置档。
type magpieProfile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
}

// magpieState 是 POST /api/state 的响应。
type magpieState struct {
	Agents   []magpieAgent   `json:"agents"`
	Clients  []any           `json:"clients"`
	Profiles []magpieProfile `json:"profiles"`
	Catalog  string          `json:"catalog"`
	Settings map[string]any  `json:"settings"`
	FX       map[string]any  `json:"fx"`
	Served   int             `json:"served"`
	// Connected 是连接动作的结果（前端读取 how）；Notice 是附加提醒。
	Connected map[string]any `json:"connected,omitempty"`
	Notice    string         `json:"notice,omitempty"`
}

// handleMagpieState 聚合返回前端需要的全部状态。
func (g *Gateway) handleMagpieState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.buildMagpieState())
}

func (g *Gateway) buildMagpieState() magpieState {
	opts := g.modelOptions()

	statuses := g.clientsManager().List()
	agents := make([]magpieAgent, 0, len(statuses))
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
		agents = append(agents, magpieAgent{
			ID:   st.ID,
			Name: st.Name,
			Icon: st.ID + "-color",
			Path: st.Path,
			Fields: []magpieField{{
				Key:     "model",
				Label:   "model",
				Value:   model,
				Options: opts,
			}},
			Wired:  wired,
			Models: map[string]any{"shown": shown, "listed": len(opts)},
		})
	}

	profiles := make([]magpieProfile, 0, 4)
	g.settingsMu.Lock()
	profSnapshot := append([]Profile(nil), g.settings.Profiles...)
	g.settingsMu.Unlock()
	for _, p := range profSnapshot {
		profiles = append(profiles, magpieProfile{ID: p.ID, Name: p.Name, UpdatedAt: p.UpdatedAt})
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
	st := magpieState{
		Agents:   agents,
		Clients:  []any{},
		Profiles: profiles,
		Catalog:  "",
		Settings: g.magpieSettings(),
		FX:       map[string]any{"rate": 7.1, "stale": true},
		Served:   served,
	}
	if len(connected) > 0 {
		st.Connected = map[string]any{"how": "joined", "agents": connected}
	}
	return st
}

// magpieSettings 是 /api/state 里的 UI 偏好：默认值 + 已保存的透传偏好。
// 前端的 applyPrefs 用 state.settings 应用主题/语言等，缺了它们刷新后
// 会回到默认值（设置页保存过的偏好看起来像没生效）。
func (g *Gateway) magpieSettings() map[string]any {
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
func (g *Gateway) modelOptions() []magpieOption {
	out := make([]magpieOption, 0, 128)

	// 路由组排在前面：它们是"逻辑模型"，用户最常选
	for _, name := range g.modelGroupNames() {
		out = append(out, magpieOption{
			Value: name,
			Label: name,
			Note:  "路由组",
			Group: "路由组",
			Ref:   name,
		})
	}

	seen := make(map[string]bool, 128)
	for _, m := range provider.AllManifests() {
		if !g.providerIsEnabled(m.ID) {
			continue
		}
		for _, model := range m.Models {
			up := model.UpstreamModel
			if up == "" {
				up = model.ID
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
			out = append(out, magpieOption{
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

// handleMagpieSync POST /api/sync —— Agents 页"刷新模型清单"的收尾：
// 返回最新 state（前端拿到后重绘 Agents 与渠道模型）。
func (g *Gateway) handleMagpieSync(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.buildMagpieState())
}

// handleMagpieSet 处理前端的"设置某个字段"动作。
//
//	POST /api/set  {"agent":"claude-code","field":"model","value":"openai-compat/xxx"}
func (g *Gateway) handleMagpieSet(w http.ResponseWriter, r *http.Request) {
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
	key := g.cfg.APIKey
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

	writeJSON(w, http.StatusOK, g.buildMagpieState())
}

/* ---------------- 渠道视图的辅助类型 ---------------- */

// magpieModelView 是渠道行里一个模型的展示形态。
type magpieModelView struct {
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
}

// magpieKeyView 是 API Key 的展示形态：只给是否设置与掩码后的样子。
type magpieKeyView struct {
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
