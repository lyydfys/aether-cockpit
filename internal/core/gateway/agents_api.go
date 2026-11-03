package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"aether-cockpit/internal/core/clients"
	"aether-cockpit/internal/core/eventbus"
)

// Agents 页的动作端点（aether agents/*）。
//
// 本项目的"agent"就是本机可注入配置的客户端（clients.Manager）：
// connect/restart/reapply 写配置，disconnect 还原备份，arrange 只排 UI 顺序，
// stage 把字段值留到下次启动，preview 目前不提供逐行 diff（前端有回退文案）。

type agentModelItem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Group  string `json:"group,omitempty"`
	Icon   string `json:"icon,omitempty"`
	Hidden bool   `json:"hidden,omitempty"`
	InUse  bool   `json:"inUse,omitempty"`
}

// agentModelsView 构建某 agent 的模型清单（隐藏/排序取自 UI 偏好）。
func (g *Gateway) agentModelsView(id string) map[string]any {
	prefs := g.agentModelPrefs(id)
	hidden := stringSet(prefs["hidden"])
	order := stringSetIndex(prefs["order"])

	items := make([]agentModelItem, 0, len(g.modelOptions()))
	seen := map[string]bool{}
	for _, o := range g.modelOptions() {
		if o.Value == "" || seen[o.Value] {
			continue
		}
		seen[o.Value] = true
		items = append(items, agentModelItem{
			ID: o.Value, Name: o.Label, Group: o.Group, Icon: o.Icon,
			Hidden: hidden[o.Value],
		})
	}
	if len(order) > 0 {
		rank := func(it agentModelItem) int {
			if i, ok := order[it.ID]; ok {
				return i
			}
			return len(order) + 1
		}
		// 稳定排序：有顺序的按顺序，其余保持原顺序。
		for i := 1; i < len(items); i++ {
			for j := i; j > 0 && rank(items[j]) < rank(items[j-1]); j-- {
				items[j], items[j-1] = items[j-1], items[j]
			}
		}
	}
	inUse := ""
	if mgr := g.clientsManager(); mgr != nil {
		for _, st := range mgr.List() {
			if st.ID == id && st.Managed != nil {
				inUse = st.Managed.Model
			}
		}
	}
	for i := range items {
		items[i].InUse = inUse != "" && items[i].ID == inUse
	}
	only, _ := prefs["only"].(bool)
	ownHidden, _ := prefs["ownHidden"].(bool)
	ordered := len(order) > 0
	return map[string]any{
		"models": items, "ordered": ordered, "only": only,
		"own": false, "ownHidden": ownHidden,
	}
}

func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	if list, ok := v.([]any); ok {
		for _, x := range list {
			if s, ok := x.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}

func stringSetIndex(v any) map[string]int {
	out := map[string]int{}
	if list, ok := v.([]any); ok {
		for i, x := range list {
			if s, ok := x.(string); ok {
				out[s] = i
			}
		}
	}
	return out
}

func (g *Gateway) agentModelPrefs(id string) map[string]any {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	all, _ := g.settings.UI["agentModels"].(map[string]any)
	if all == nil {
		return map[string]any{}
	}
	if one, ok := all[id].(map[string]any); ok {
		return one
	}
	return map[string]any{}
}

func (g *Gateway) saveAgentModelPrefs(id string, prefs map[string]any) error {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.UI == nil {
		g.settings.UI = map[string]any{}
	}
	all, _ := g.settings.UI["agentModels"].(map[string]any)
	if all == nil {
		all = map[string]any{}
		g.settings.UI["agentModels"] = all
	}
	all[id] = prefs
	return g.saveSettings()
}

func (g *Gateway) handleAgentModels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, g.agentModelsView(id))
		return
	}
	var body struct {
		Hidden    *[]string `json:"hidden"`
		Shown     *[]string `json:"shown"`
		Order     *[]string `json:"order"`
		Only      *bool     `json:"only"`
		OwnHidden *bool     `json:"ownHidden"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败"})
		return
	}
	prefs := g.agentModelPrefs(id)
	if body.Shown != nil {
		shown := map[string]bool{}
		for _, x := range *body.Shown {
			shown[x] = true
		}
		hidden := []any{}
		seen := map[string]bool{}
		for _, it := range g.agentModelsView(id)["models"].([]agentModelItem) {
			if !shown[it.ID] && !seen[it.ID] {
				hidden = append(hidden, it.ID)
				seen[it.ID] = true
			}
		}
		prefs["hidden"] = hidden
	} else if body.Hidden != nil {
		list := make([]any, 0, len(*body.Hidden))
		for _, x := range *body.Hidden {
			list = append(list, x)
		}
		prefs["hidden"] = list
	}
	if body.Order != nil {
		list := make([]any, 0, len(*body.Order))
		for _, x := range *body.Order {
			list = append(list, x)
		}
		prefs["order"] = list
	}
	if body.Only != nil {
		prefs["only"] = *body.Only
	}
	if body.OwnHidden != nil {
		prefs["ownHidden"] = *body.OwnHidden
	}
	if err := g.saveAgentModelPrefs(id, prefs); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, g.agentModelsView(id))
}

// handleAgentAction POST /api/agents/{action}[/{id}]
func (g *Gateway) handleAgentAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	id := r.PathValue("id")
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))

	switch action {
	case "rescan":
		writeJSON(w, http.StatusOK, g.buildAetherState())

	case "arrange":
		var body struct {
			Order  []string `json:"order"`
			Hidden []string `json:"hidden"`
			Shown  []string `json:"shown"`
		}
		_ = json.Unmarshal(raw, &body)
		g.settingsMu.Lock()
		if g.settings.UI == nil {
			g.settings.UI = map[string]any{}
		}
		g.settings.UI["agentOrder"] = body.Order
		g.settings.UI["agentsHidden"] = body.Hidden
		g.settings.UI["agentsShown"] = body.Shown
		saveErr := g.saveSettings()
		g.settingsMu.Unlock()
		if saveErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + saveErr.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"agentOrder": body.Order, "agentsHidden": body.Hidden, "agentsShown": body.Shown,
		})

	case "connect", "restart", "reapply":
		if err := g.applyAgent(id, r); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		st := g.buildAetherState()
		if action == "restart" {
			st.Notice = "配置已重写；正在运行的进程需要重启后才会读取"
		}
		writeJSON(w, http.StatusOK, st)

	case "disconnect", "disconnect-offline":
		mgr := g.clientsManager()
		if mgr == nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "客户端配置管理不可用"})
			return
		}
		if err := mgr.Restore(id, ""); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		eventbus.Emit(eventbus.TopicSpec+".updated", map[string]any{"source": "agent_disconnect", "client": id})
		writeJSON(w, http.StatusOK, g.buildAetherState())

	case "keep":
		// "保持当前设置"：不写配置，但记住"这个差异是我有意保留的"，
		// 之后不再报同一条漂移；配置再变化（摘要变了）时会重新提示。
		//
		// 以前这里只回一次 state，前端清掉提示后下次刷新又冒出来——
		// 用户点十次也没用，等于没有这个动作。
		mgr := g.clientsManager()
		if mgr == nil {
			writeJSON(w, http.StatusOK, g.buildAetherState())
			return
		}
		origin := requestOrigin(r, g.PublicAddr())
		key := strings.TrimSpace(g.cfg.APIKey)
		if key == "" {
			key = "aether-local"
		}
		digest := ""
		for _, a := range g.buildAetherState().Agents {
			if a.ID != id {
				continue
			}
			if changes, err := mgr.PreviewApply(a.ID, g.driftParamsFor(a, origin, key)); err == nil {
				digest = driftDigest(changes)
			}
			break
		}
		if err := g.markDriftKept(id, digest); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, g.buildAetherState())

	case "stage":
		var body struct {
			Field string          `json:"field"`
			Value json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || body.Field == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 field"})
			return
		}
		g.settingsMu.Lock()
		if g.settings.UI == nil {
			g.settings.UI = map[string]any{}
		}
		all, _ := g.settings.UI["agentStage"].(map[string]any)
		if all == nil {
			all = map[string]any{}
			g.settings.UI["agentStage"] = all
		}
		one, _ := all[id].(map[string]any)
		if one == nil {
			one = map[string]any{}
			all[id] = one
		}
		var val any
		_ = json.Unmarshal(body.Value, &val)
		one[body.Field] = val
		saveErr := g.saveSettings()
		g.settingsMu.Unlock()
		if saveErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + saveErr.Error()})
			return
		}
		writeJSON(w, http.StatusOK, g.buildAetherState())

	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知 Agent 动作: " + action})
	}
}

// applyAgent 把网关地址与当前模型写进客户端配置。
func (g *Gateway) applyAgent(id string, r *http.Request) error {
	mgr := g.clientsManager()
	if mgr == nil {
		return errString("客户端配置管理不可用")
	}
	model := ""
	if sts := mgr.List(); sts != nil {
		for _, st := range sts {
			if st.ID == id && st.Managed != nil {
				model = st.Managed.Model
			}
		}
	}
	if model == "" {
		if opts := g.modelOptions(); len(opts) > 0 {
			model = opts[0].Value
		}
	}
	origin, err := g.clientOriginFor(r, mgr, id)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(g.cfg.APIKey)
	if key == "" {
		key = "aether-local"
	}
	if _, err := mgr.Apply(id, clients.ApplyParams{
		BaseURL: origin, APIKey: key, Model: model, ModelOptions: g.agentModelOptions(id),
	}); err != nil {
		return err
	}
	eventbus.Emit(eventbus.TopicSpec+".updated", map[string]any{"source": "agent_connect", "client": id})
	return nil
}

// agentModelOptions 是该 Agent 在 Agents 页可见/排序后的模型清单，
// 写进 Claude Code 的 modelPicker（隐藏与排序随之生效）。
func (g *Gateway) agentModelOptions(id string) []clients.ModelOption {
	hidden := g.hiddenModelsForAgent(id)
	order := g.modelOrderForAgent(id)
	opts := make([]aetherOption, 0, len(g.modelOptions()))
	for _, o := range g.modelOptions() {
		if o.Value == "" || hidden[o.Value] {
			continue
		}
		opts = append(opts, o)
	}
	sort.SliceStable(opts, func(i, j int) bool { return orderLess(order, opts[i].Value, opts[j].Value) })
	out := make([]clients.ModelOption, 0, len(opts))
	for _, o := range opts {
		desc := o.Note
		if desc == "" {
			desc = "Aether Cockpit"
		}
		out = append(out, clients.ModelOption{Value: o.Value, Label: o.Label, Description: desc})
	}
	return out
}

func (g *Gateway) handleAgentPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mgr := g.clientsManager()
	if mgr == nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": "客户端配置管理不可用", "changes": []any{}})
		return
	}
	st, ok := mgr.Lookup(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "未知 Agent: " + id})
		return
	}
	var changes []clients.PreviewChange
	var err error
	if st.Managed != nil && st.Managed.BaseURL != "" {
		// 已接入：预览"断开"会还原成什么。
		changes, err = mgr.PreviewRestore(id)
	} else {
		model := ""
		if opts := g.modelOptions(); len(opts) > 0 {
			model = opts[0].Value
		}
		origin := requestOrigin(r, g.PublicAddr())
		key := strings.TrimSpace(g.cfg.APIKey)
		if key == "" {
			key = "aether-local"
		}
		changes, err = mgr.PreviewApply(id, clients.ApplyParams{
			BaseURL: origin, APIKey: key, Model: model, ModelOptions: g.agentModelOptions(id),
		})
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error(), "changes": []any{}, "revision": ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes, "revision": ""})
}

func (g *Gateway) handleAgentFast(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err := json.Unmarshal(raw, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败"})
		return
	}
	g.settingsMu.Lock()
	if g.settings.UI == nil {
		g.settings.UI = map[string]any{}
	}
	all, _ := g.settings.UI["agentFast"].(map[string]any)
	if all == nil {
		all = map[string]any{}
		g.settings.UI["agentFast"] = all
	}
	ref, _ := body["ref"].(string)
	if ref == "" {
		ref = "default"
	}
	all[ref] = body
	saveErr := g.saveSettings()
	g.settingsMu.Unlock()
	if saveErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存失败: " + saveErr.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type errString string

func (e errString) Error() string { return string(e) }
