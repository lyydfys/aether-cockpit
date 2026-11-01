package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"aether-cockpit/internal/provider"
)

// groupPayload 是保存/导入一个路由组的请求体（与 magpie 前端字段一致）。
type groupPayload struct {
	ID         string           `json:"id"`
	From       string           `json:"from"`
	Name       string           `json:"name"`
	Members    []string         `json:"members"`
	Match      []string         `json:"match"`
	Routing    string           `json:"routing"`
	Pick       string           `json:"pick"`
	Affinity   string           `json:"affinity"`
	Fast       []string         `json:"fast"`
	Off        []string         `json:"off"`
	Levels     []string         `json:"levels"`
	Weights    map[string]int   `json:"weights"`
	Rules      []map[string]any `json:"rules"`
	Effort     string           `json:"effort"`
	Classifier string           `json:"classifier"`
	Family     string           `json:"family"`
	FirstToken int              `json:"firstToken"`
	Patience   int              `json:"patience"`
	Context    int              `json:"context"`
	Sink       bool             `json:"sink"`
	Disabled   bool             `json:"disabled"`
	Hidden     bool             `json:"hidden"`
	Unlists    bool             `json:"unlists"`
}

func chainFromMembers(members []string) []string {
	var chain []string
	for _, id := range members {
		pid, _, ok := strings.Cut(id, "/")
		if !ok || pid == "" || containsString(chain, pid) {
			continue
		}
		if _, registered := provider.Get(pid); !registered {
			continue
		}
		chain = append(chain, pid)
	}
	return chain
}

// defFromPayload 把页面请求折成组定义。
func defFromPayload(p groupPayload, id string) routingGroupDef {
	members := dedupeStrings(p.Members)
	def := routingGroupDef{
		ID: id, Name: strings.TrimSpace(p.Name), Members: members,
		Chain: chainFromMembers(members), Routing: p.Routing, Pick: p.Pick,
		Fast: p.Fast, Off: p.Off, Levels: p.Levels, Match: p.Match,
		Weights: p.Weights, Rules: p.Rules, Disabled: p.Disabled, Hidden: p.Hidden, Unlists: p.Unlists,
	}
	if def.Name == "" {
		def.Name = id
	}
	if def.Routing == "manual" && def.Pick != "" {
		if pid, _, ok := strings.Cut(def.Pick, "/"); ok {
			def.Chain = moveProviderFirst(def.Chain, pid)
		}
	}
	return def
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func (g *Gateway) persistGroup(def routingGroupDef, oldID string) error {
	if len(def.Chain) == 0 {
		return fmt.Errorf("至少选择一个成员模型")
	}
	obj := map[string]any{
		"chain":    def.Chain,
		"strategy": groupStrategyFor(def.Routing),
		"name":     def.Name,
		"members":  def.Members,
		"routing":  def.Routing,
	}
	if def.Pick != "" {
		obj["pick"] = def.Pick
	}
	if len(def.Fast) > 0 {
		obj["fast"] = def.Fast
	}
	if len(def.Off) > 0 {
		obj["off"] = def.Off
	}
	if len(def.Levels) > 0 {
		obj["levels"] = def.Levels
	}
	if len(def.Match) > 0 {
		obj["match"] = def.Match
	}
	if len(def.Weights) > 0 {
		obj["weights"] = def.Weights
	}
	if len(def.Rules) > 0 {
		obj["rules"] = def.Rules
	}
	if def.Disabled {
		obj["disabled"] = true
	}
	if def.Hidden {
		obj["hidden"] = true
	}
	if def.Unlists {
		obj["unlists"] = true
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return err
	}

	g.settingsMu.Lock()
	if g.settings.ModelGroupsOverride == nil {
		g.settings.ModelGroupsOverride = map[string]json.RawMessage{}
	}
	if oldID != "" && oldID != def.ID {
		delete(g.settings.ModelGroupsOverride, oldID)
		g.settings.DeletedGroups = removeString(g.settings.DeletedGroups, oldID)
	}
	g.settings.ModelGroupsOverride[def.ID] = raw
	g.settings.DeletedGroups = removeString(g.settings.DeletedGroups, def.ID)
	order := make([]string, 0, len(g.settings.RoutingGroupOrder)+1)
	placed := false
	for _, id := range g.settings.RoutingGroupOrder {
		if id == oldID && oldID != def.ID {
			continue
		}
		if id == def.ID {
			placed = true
		}
		order = append(order, id)
	}
	if !placed {
		order = append(order, def.ID)
	}
	g.settings.RoutingGroupOrder = order
	saveErr := g.saveSettings()
	g.settingsMu.Unlock()
	g.rebuildRoutingGroups()
	return saveErr
}

func removeString(list []string, v string) []string {
	out := list[:0]
	for _, s := range list {
		if s != v {
			out = append(out, s)
		}
	}
	return out
}

func (g *Gateway) saveDeleted(ids []string) error {
	g.settingsMu.Lock()
	for _, id := range ids {
		if g.settings.ModelGroupsOverride != nil {
			delete(g.settings.ModelGroupsOverride, id)
		}
		if !containsString(g.settings.DeletedGroups, id) {
			g.settings.DeletedGroups = append(g.settings.DeletedGroups, id)
		}
		g.settings.RoutingGroupOrder = removeString(g.settings.RoutingGroupOrder, id)
	}
	saveErr := g.saveSettings()
	g.settingsMu.Unlock()
	g.rebuildRoutingGroups()
	return saveErr
}

// handleGroupAction 处理 POST /api/groups/{action}。
func (g *Gateway) handleGroupAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))

	fail := func(code int, msg string) {
		writeJSON(w, code, map[string]any{"error": msg})
	}

	switch action {
	case "save":
		var p groupPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			fail(http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		id := strings.TrimSpace(p.ID)
		if id == "" {
			id = slugify(p.Name)
		}
		if id == "" {
			fail(http.StatusBadRequest, "组名不能为空")
			return
		}
		if id != p.From {
			for _, def := range g.groupDefsForView() {
				if def.ID == id && def.ID != p.From {
					fail(http.StatusConflict, "已有同名路由组: "+id)
					return
				}
			}
		}
		def := defFromPayload(p, id)
		if len(def.Chain) == 0 {
			fail(http.StatusBadRequest, "至少选择一个成员模型")
			return
		}
		if err := g.persistGroup(def, p.From); err != nil {
			fail(http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, g.buildGroups())

	case "delete":
		var body struct {
			ID  string   `json:"id"`
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			fail(http.StatusBadRequest, "请求体解析失败")
			return
		}
		ids := body.IDs
		if body.ID != "" {
			ids = append(ids, body.ID)
		}
		if len(ids) == 0 {
			fail(http.StatusBadRequest, "缺少 id")
			return
		}
		if err := g.saveDeleted(dedupeStrings(ids)); err != nil {
			fail(http.StatusInternalServerError, "删除失败: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, g.buildGroups())

	case "arrange":
		var body struct {
			Order []string `json:"order"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || len(body.Order) == 0 {
			fail(http.StatusBadRequest, "缺少 order")
			return
		}
		g.settingsMu.Lock()
		g.settings.RoutingGroupOrder = dedupeStrings(body.Order)
		saveErr := g.saveSettings()
		g.settingsMu.Unlock()
		if saveErr != nil {
			fail(http.StatusInternalServerError, "保存失败: "+saveErr.Error())
			return
		}
		g.rebuildRoutingGroups()
		writeJSON(w, http.StatusOK, g.buildGroups())

	case "pick":
		var body struct {
			ID   string `json:"id"`
			Pick string `json:"pick"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || body.ID == "" || body.Pick == "" {
			fail(http.StatusBadRequest, "缺少 id 或 pick")
			return
		}
		var target *routingGroupDef
		for _, def := range g.groupDefsForView() {
			if def.ID == body.ID {
				d := def
				target = &d
				break
			}
		}
		if target == nil {
			fail(http.StatusNotFound, "路由组不存在: "+body.ID)
			return
		}
		if !containsString(target.Members, body.Pick) {
			fail(http.StatusBadRequest, "pick 不在该组的成员里")
			return
		}
		target.Pick = body.Pick
		target.Routing = "manual"
		if err := g.persistGroup(*target, target.ID); err != nil {
			fail(http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, g.buildGroups())

	case "found":
		var body struct {
			On bool `json:"on"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			fail(http.StatusBadRequest, "请求体解析失败")
			return
		}
		on := body.On
		g.settingsMu.Lock()
		g.settings.FoundGroups = &on
		saveErr := g.saveSettings()
		g.settingsMu.Unlock()
		if saveErr != nil {
			fail(http.StatusInternalServerError, "保存失败: "+saveErr.Error())
			return
		}
		g.rebuildRoutingGroups()
		writeJSON(w, http.StatusOK, g.buildGroups())

	case "share":
		var body struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &body)
		var target *routingGroupDef
		for _, def := range g.groupDefsForView() {
			if def.ID == body.ID {
				d := def
				target = &d
				break
			}
		}
		if target == nil {
			fail(http.StatusNotFound, "路由组不存在: "+body.ID)
			return
		}
		shared, _ := json.Marshal(map[string]any{"aether_group": map[string]any{
			"name": target.Name, "members": target.Members, "routing": target.Routing,
			"pick": target.Pick, "fast": target.Fast, "off": target.Off,
			"weights": target.Weights, "rules": target.Rules, "match": target.Match,
		}})
		writeJSON(w, http.StatusOK, map[string]any{"shared": string(shared)})

	case "import":
		var body struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &body); err != nil || strings.TrimSpace(body.Text) == "" {
			fail(http.StatusBadRequest, "粘贴的内容为空")
			return
		}
		var wrap struct {
			AetherGroup json.RawMessage `json:"aether_group"`
		}
		text := []byte(strings.TrimSpace(body.Text))
		if json.Unmarshal(text, &wrap) == nil && len(wrap.AetherGroup) > 0 {
			text = wrap.AetherGroup
		}
		var p groupPayload
		if err := json.Unmarshal(text, &p); err != nil {
			fail(http.StatusBadRequest, "不是有效的路由组分享文本")
			return
		}
		existing := map[string]bool{}
		for _, def := range g.groupDefsForView() {
			existing[def.ID] = true
		}
		name := strings.TrimSpace(p.Name)
		if name == "" {
			name = "imported"
		}
		id := slugify(name)
		if id == "" {
			id = "imported"
		}
		for i := 2; existing[id]; i++ {
			id = fmt.Sprintf("%s-%d", slugify(name), i)
		}
		p.Name = name
		def := defFromPayload(p, id)
		if len(def.Chain) == 0 {
			fail(http.StatusBadRequest, "分享里的模型在本机都没有对应渠道")
			return
		}
		if err := g.persistGroup(def, ""); err != nil {
			fail(http.StatusInternalServerError, "导入失败: "+err.Error())
			return
		}
		out := g.buildGroups()
		out["imported"] = map[string]any{"id": id, "name": name}
		writeJSON(w, http.StatusOK, out)

	case "template":
		var body struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(raw, &body)
		members, err := g.templateMembers(body.Kind)
		if err != nil {
			fail(http.StatusBadRequest, err.Error())
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			name = body.Kind
		}
		id := slugify(name)
		routing := "order"
		if body.Kind == "smart" {
			routing = ""
		}
		p := groupPayload{Name: name, Members: members, Routing: routing}
		if err := g.persistGroup(defFromPayload(p, id), ""); err != nil {
			fail(http.StatusInternalServerError, "创建模板失败: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, g.buildGroups())

	default:
		fail(http.StatusNotFound, "未知路由组动作: "+action)
	}
}

// templateMembers 按模板挑选成员：steady 每渠道一个，thrifty 优先便宜模型，
// smart 用"强模型 + 便宜模型"并要求已配置意图分类模型。
func (g *Gateway) templateMembers(kind string) ([]string, error) {
	switch kind {
	case "steady":
		var out []string
		for _, m := range provider.AllManifests() {
			var pick string
			for _, mm := range m.Models {
				up := mm.UpstreamModel
				if up == "" {
					up = mm.ID
				}
				if strings.EqualFold(up, "auto") {
					continue
				}
				pick = m.ID + "/" + up
				break
			}
			if pick != "" {
				out = append(out, pick)
			}
		}
		if len(out) < 2 {
			return nil, fmt.Errorf("模板需要至少两个渠道的模型")
		}
		return out, nil
	case "thrifty":
		var out []string
		for _, m := range provider.AllManifests() {
			for _, mm := range m.Models {
				up := mm.UpstreamModel
				if up == "" {
					up = mm.ID
				}
				lower := strings.ToLower(up)
				for _, kw := range []string{"flash", "mini", "lite", "free", "efficient", "economy", "air"} {
					if strings.Contains(lower, kw) {
						out = append(out, m.ID+"/"+up)
						break
					}
				}
			}
		}
		sort.Strings(out)
		if len(out) < 2 {
			return nil, fmt.Errorf("没有足够的便宜模型可以组成 Cheap first")
		}
		if len(out) > 4 {
			out = out[:4]
		}
		return out, nil
	case "smart":
		if strings.TrimSpace(g.cfg.IntentClassifyModel) == "" {
			return nil, fmt.Errorf("Smart 模板需要先在配置里设置 intent_classify_model（意图分类模型），再建组")
		}
		strong, cheap := smartMembers()
		if len(strong) == 0 || len(cheap) == 0 {
			return nil, fmt.Errorf("Smart 模板需要至少一个强模型和一个便宜模型")
		}
		out := append([]string{}, strong...)
		out = append(out, cheap...)
		return out, nil
	}
	return nil, fmt.Errorf("未知模板: %s", kind)
}

// steadyMembers 每个渠道挑一个非 auto 模型（优先列表里第一个）。
func steadyMembers() []string {
	var out []string
	for _, m := range provider.AllManifests() {
		for _, mm := range m.Models {
			up := mm.UpstreamModel
			if up == "" {
				up = mm.ID
			}
			if strings.EqualFold(up, "auto") {
				continue
			}
			out = append(out, m.ID+"/"+up)
			break
		}
	}
	return out
}

// cheapMembers 是名字带 flash/mini/lite/free/efficient/economy/air 的模型。
func cheapMembers() []string {
	var out []string
	for _, m := range provider.AllManifests() {
		for _, mm := range m.Models {
			up := mm.UpstreamModel
			if up == "" {
				up = mm.ID
			}
			lower := strings.ToLower(up)
			for _, kw := range []string{"flash", "mini", "lite", "free", "efficient", "economy", "air"} {
				if strings.Contains(lower, kw) {
					out = append(out, m.ID+"/"+up)
					break
				}
			}
		}
	}
	sort.Strings(out)
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

// smartMembers 返回强模型（每渠道第一个非便宜、非 auto 的模型）与便宜模型。
func smartMembers() (strong, cheap []string) {
	cheapSet := map[string]bool{}
	for _, id := range cheapMembers() {
		cheapSet[id] = true
	}
	seen := map[string]bool{}
	for _, m := range provider.AllManifests() {
		for _, mm := range m.Models {
			up := mm.UpstreamModel
			if up == "" {
				up = mm.ID
			}
			id := m.ID + "/" + up
			if strings.EqualFold(up, "auto") || cheapSet[id] || seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			strong = append(strong, id)
			break
		}
	}
	cheap = cheapMembers()
	return
}

// templatesView 是 /api/groups 的 templates（前端模板菜单/引导用）。
// 不能建成的模板带 need，前端会禁用并说明原因。
func (g *Gateway) templatesView() []map[string]any {
	type tpl struct {
		kind, name, routing string
		members             []string
		need, of            string
	}
	steady := steadyMembers()
	thrifty := cheapMembers()
	tpls := []tpl{
		{kind: "steady", name: "永不卡住", routing: "order", members: steady},
		{kind: "thrifty", name: "便宜优先", routing: "order", members: thrifty},
	}
	if strings.TrimSpace(g.cfg.IntentClassifyModel) != "" {
		strong, cheap := smartMembers()
		members := append([]string{}, strong...)
		members = append(members, cheap...)
		tpls = append(tpls, tpl{kind: "smart", name: "难的给强的", routing: "", members: members})
	}
	out := make([]map[string]any, 0, len(tpls))
	for _, t := range tpls {
		need, of := t.need, t.of
		if len(t.members) == 0 {
			need = "providers"
			of = ""
		} else if t.kind == "steady" && len(t.members) < 2 {
			need, of = "providers", t.members[0]
		} else if t.kind == "thrifty" && len(t.members) < 2 {
			need, of = "cheap", ""
		}
		gm := map[string]any{
			"id": "template/" + t.kind, "name": t.name, "routing": t.routing,
			"members": t.members, "auto": false,
		}
		row := map[string]any{"kind": t.kind, "group": gm}
		if need != "" {
			row["need"] = need
			if of != "" {
				row["of"] = of
			}
		}
		out = append(out, row)
	}
	return out
}
