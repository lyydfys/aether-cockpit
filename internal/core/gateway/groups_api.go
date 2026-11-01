package gateway

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"aether-cockpit/internal/provider"
)

// 路由组 CRUD（对齐 aether Routing 页的 groups/* 动作）。
//
// 组的定义存在 settings.model_groups_override 里：chain 给路由器用，
// members / routing / pick / weights 等给页面原样回显。基线组来自
// cockpit.json 的 model_groups，删除后由 deleted_groups 记住，避免复活。
// "自动发现"组（auto/…）不落盘：每次按"多个渠道都提供的同一模型"重算。

// routingMember 是路由组成员的展示信息。
type routingMember struct {
	ID           string  `json:"id"`
	Name         string  `json:"name,omitempty"`
	Provider     string  `json:"provider,omitempty"`
	ProviderName string  `json:"providerName,omitempty"`
	Icon         string  `json:"icon,omitempty"`
	ProviderOff  bool    `json:"providerOff,omitempty"`
	CreditRate   float64 `json:"credit_rate,omitempty"`
}

// routingGroupDef 是内部统一的组定义。
type routingGroupDef struct {
	ID       string
	Name     string
	Members  []string
	Chain    []string
	Routing  string
	Pick     string
	Auto     bool
	Fast     []string
	Off      []string
	Levels   []string
	Match    []string
	Weights  map[string]int
	Rules    []map[string]any
	Disabled bool
	Hidden   bool
	Unlists  bool
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	last := false
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			last = false
		} else if !last && b.Len() > 0 {
			b.WriteByte('-')
			last = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// groupStrategyFor 把页面上的路由方式映射到内核策略。
func groupStrategyFor(routing string) string {
	switch routing {
	case "rotate":
		return GroupStrategyRotate
	case "usage":
		return GroupStrategyPace
	case "pace":
		return GroupStrategyPace
	case "fast":
		return GroupStrategyFast
	case "", "smart":
		return GroupStrategySmart
	default: // order / free / weight / manual
		return GroupStrategyPriority
	}
}

func routingLabelFor(strategy string) string {
	switch strategy {
	case "rotate":
		return "rotate"
	case "usage":
		return "usage"
	case "pace":
		return "pace"
	case "fast":
		return "fast"
	case "smart":
		return ""
	}
	return "order"
}

func (g *Gateway) foundGroupsOn() bool {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	return g.settings.FoundGroups == nil || *g.settings.FoundGroups
}

func (g *Gateway) groupTombstones() map[string]bool {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	out := make(map[string]bool, len(g.settings.DeletedGroups))
	for _, id := range g.settings.DeletedGroups {
		out[id] = true
	}
	return out
}

// rebuildRoutingGroups 按基线 + 覆盖 + 自动发现重建 ModelRouter 里的虚拟组。
func (g *Gateway) rebuildRoutingGroups() {
	g.settingsMu.Lock()
	override := make(map[string]json.RawMessage, len(g.settings.ModelGroupsOverride))
	for k, v := range g.settings.ModelGroupsOverride {
		override[k] = v
	}
	deleted := make(map[string]bool, len(g.settings.DeletedGroups))
	for _, id := range g.settings.DeletedGroups {
		deleted[id] = true
	}
	found := g.settings.FoundGroups == nil || *g.settings.FoundGroups
	g.settingsMu.Unlock()

	merged := make(map[string]json.RawMessage, len(g.cfg.ModelGroups)+len(override))
	for name, raw := range g.cfg.ModelGroups {
		merged[name] = raw
	}
	for name, raw := range override {
		merged[name] = raw
	}

	g.models.ClearVirtualGroups()
	for name, raw := range merged {
		if deleted[name] {
			continue
		}
		chain, strategy, err := parseModelGroup(raw)
		if err != nil {
			continue
		}
		var def routingGroupDef
		_ = json.Unmarshal(raw, &def)
		if def.Routing != "" {
			strategy = groupStrategyFor(def.Routing)
		}
		if def.Routing == "manual" && def.Pick != "" {
			if pid, _, ok := strings.Cut(def.Pick, "/"); ok {
				chain = moveProviderFirst(chain, pid)
			}
		}
		valid := make([]string, 0, len(chain))
		for _, pid := range chain {
			if _, ok := provider.Get(pid); ok {
				valid = append(valid, pid)
			}
		}
		if len(valid) > 0 {
			g.models.SetVirtualGroupWithStrategy(name, valid, strategy)
		}
	}
	if found {
		for _, def := range g.autoGroupDefs() {
			if _, exists := merged[def.ID]; exists || deleted[def.ID] {
				continue
			}
			g.models.SetVirtualGroupWithStrategy(def.ID, def.Chain, GroupStrategyPriority)
		}
	}
}

func moveProviderFirst(chain []string, pid string) []string {
	out := make([]string, 0, len(chain))
	for _, p := range chain {
		if p == pid {
			out = append(out, p)
		}
	}
	for _, p := range chain {
		if p != pid {
			out = append(out, p)
		}
	}
	return out
}

// autoGroupDefs 是"同一模型多个渠道都能服务"的自动组。
func (g *Gateway) autoGroupDefs() []routingGroupDef {
	return autoGroupDefsFrom(provider.AllManifests())
}

// autoGroupDefsFrom 从给定的渠道清单算自动组（纯函数，便于测试）。
func autoGroupDefsFrom(manifests []provider.ProviderManifest) []routingGroupDef {
	type acc struct {
		providers []string
		members   []string
	}
	byBare := map[string]*acc{}
	for _, m := range manifests {
		for _, mm := range m.Models {
			up := mm.UpstreamModel
			if up == "" {
				up = mm.ID
			}
			bare := up
			if i := strings.LastIndex(bare, "/"); i >= 0 {
				bare = bare[i+1:]
			}
			a := byBare[bare]
			if a == nil {
				a = &acc{}
				byBare[bare] = a
			}
			if !containsString(a.providers, m.ID) {
				a.providers = append(a.providers, m.ID)
				a.members = append(a.members, m.ID+"/"+up)
			}
		}
	}
	out := make([]routingGroupDef, 0, 8)
	for bare, a := range byBare {
		if len(a.providers) < 2 {
			continue
		}
		out = append(out, routingGroupDef{
			ID: "auto/" + bare, Name: bare, Members: a.members, Chain: a.providers, Auto: true, Routing: "order",
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// groupDefsForView 汇总页面要展示的全部组（有序）。
func (g *Gateway) groupDefsForView() []routingGroupDef {
	g.settingsMu.Lock()
	override := make(map[string]json.RawMessage, len(g.settings.ModelGroupsOverride))
	for k, v := range g.settings.ModelGroupsOverride {
		override[k] = v
	}
	deleted := make(map[string]bool, len(g.settings.DeletedGroups))
	for _, id := range g.settings.DeletedGroups {
		deleted[id] = true
	}
	order := append([]string(nil), g.settings.RoutingGroupOrder...)
	found := g.settings.FoundGroups == nil || *g.settings.FoundGroups
	g.settingsMu.Unlock()

	byID := map[string]routingGroupDef{}
	add := func(id string, raw json.RawMessage) {
		if deleted[id] {
			return
		}
		chain, strategy, err := parseModelGroup(raw)
		if err != nil {
			return
		}
		var def routingGroupDef
		_ = json.Unmarshal(raw, &def)
		def.ID = id
		if def.Name == "" {
			def.Name = id
		}
		def.Chain = chain
		if def.Routing == "" {
			def.Routing = routingLabelFor(strategy)
		}
		if def.Members == nil {
			def.Members = expandChainMembers(chain)
		}
		byID[id] = def
	}
	for name, raw := range g.cfg.ModelGroups {
		add(name, raw)
	}
	for name, raw := range override {
		add(name, raw)
	}
	if found {
		for _, def := range g.autoGroupDefs() {
			if _, exists := byID[def.ID]; exists || deleted[def.ID] {
				continue
			}
			byID[def.ID] = def
		}
	}

	defs := make([]routingGroupDef, 0, len(byID))
	seen := map[string]bool{}
	for _, id := range order {
		if def, ok := byID[id]; ok && !seen[id] {
			seen[id] = true
			defs = append(defs, def)
		}
	}
	rest := make([]routingGroupDef, 0, len(byID))
	for id, def := range byID {
		if !seen[id] {
			rest = append(rest, def)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].ID < rest[j].ID })
	return append(defs, rest...)
}

func expandChainMembers(chain []string) []string {
	var out []string
	for _, pid := range chain {
		m, ok := provider.Get(pid)
		if !ok {
			continue
		}
		for _, mm := range m.Manifest().Models {
			up := mm.UpstreamModel
			if up == "" {
				up = mm.ID
			}
			out = append(out, pid+"/"+up)
		}
	}
	return out
}

// handleGroups 返回全部路由组与可选模型。
func (g *Gateway) handleGroups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, g.buildGroups())
}

func (g *Gateway) buildGroups() map[string]any {
	models := make([]routingMember, 0, 160)
	byModel := make(map[string]routingMember, 160)
	providerOff := make(map[string]bool, 32)
	for _, m := range provider.AllManifests() {
		ready := false
		for _, e := range g.pool.ListByProvider(m.ID) {
			if e.Available() {
				ready = true
				break
			}
		}
		providerOff[m.ID] = !ready
		creditRates := g.providerModelCreditMap(m.ID)
		for _, mm := range m.Models {
			up := mm.UpstreamModel
			if up == "" {
				up = mm.ID
			}
			id := m.ID + "/" + up
			name := mm.DisplayName
			if name == "" {
				name = up
			}
			mv := routingMember{
				ID: id, Name: name, Provider: m.ID, ProviderName: m.DisplayName,
				ProviderOff: !ready, CreditRate: creditRates[up],
			}
			models = append(models, mv)
			byModel[id] = mv
		}
	}

	defs := g.groupDefsForView()
	groups := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		members := make([]string, 0, len(def.Members))
		infos := make([]routingMember, 0, len(def.Members))
		ready := false
		for _, id := range def.Members {
			members = append(members, id)
			if info, ok := byModel[id]; ok {
				infos = append(infos, info)
				if !info.ProviderOff {
					ready = true
				}
			}
		}
		gm := map[string]any{
			"id": def.ID, "name": def.Name, "members": members, "memberInfo": infos,
			"routing": def.Routing, "ready": ready, "auto": def.Auto,
		}
		if def.Pick != "" {
			gm["pick"] = def.Pick
		}
		if def.Disabled {
			gm["disabled"] = true
		}
		if def.Hidden {
			gm["hidden"] = true
		}
		if def.Unlists {
			gm["unlists"] = true
		}
		if len(def.Fast) > 0 {
			gm["fast"] = def.Fast
		}
		if len(def.Off) > 0 {
			gm["off"] = def.Off
		}
		if len(def.Levels) > 0 {
			gm["levels"] = def.Levels
		}
		if len(def.Match) > 0 {
			gm["match"] = def.Match
		}
		if len(def.Weights) > 0 {
			gm["weights"] = def.Weights
		}
		if len(def.Rules) > 0 {
			gm["rules"] = def.Rules
		}
		groups = append(groups, gm)
	}

	return map[string]any{
		"groups":    groups,
		"models":    models,
		"deciders":  []any{},
		"pools":     []any{},
		"templates": g.templatesView(),
		"found":     g.foundGroupsOn(),
	}
}
