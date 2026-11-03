package gateway

import (
	"encoding/json"
	"fmt"
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
	Affinity string
	Auto     bool
	Fast     []string
	Off      []string
	Levels   []string
	Match    []string
	Weights  map[string]int
	// Rules 是组规则（intent / effort / time），见 group_rule.go。
	Rules    []GroupRule
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

// groupStrategySupport 把页面上的路由方式映射到内核策略。
//
// 第二个返回值表示该方式是否真的被内核实现。free/weight/cheap/fastest
// 这类尚未接入路由器的选项不能静默伪装成 priority：调用方必须把它
// 作为 warning 返回给页面，或拒绝保存。
func groupStrategySupport(routing string) (string, bool) {
	switch routing {
	case "rotate":
		return GroupStrategyRotate, true
	case "usage":
		// usage 与 pace 是两种语义：usage 看剩余量（保住小号做兜底），
		// pace 看"剩余 / 到期时间"（先花临期额度）。以前两者都被折成 pace。
		return GroupStrategyUsage, true
	case "pace":
		return GroupStrategyPace, true
	case "fast":
		return GroupStrategyFast, true
	case "", "smart":
		return GroupStrategySmart, true
	case "order", "manual":
		// order 就是 priority；manual 额外用 pick 把指定渠道移到链首。
		return GroupStrategyPriority, true
	default: // free / weight / cheap / fastest / ...
		return GroupStrategyPriority, false
	}
}

func groupStrategyFor(routing string) string {
	strategy, _ := groupStrategySupport(routing)
	return strategy
}

// routingWarnings 返回页面必须展示的「配置未生效」提示。
// 只对内核确实没读的字段报警，不能把已经接入的能力（rules）也报成未接入——
// 那会让用户以为改了没用。
func routingWarnings(def routingGroupDef) []string {
	var out []string
	if _, ok := groupStrategySupport(def.Routing); !ok && def.Routing != "" {
		out = append(out, fmt.Sprintf("路由方式 %q 尚未实现，当前按优先级链执行", def.Routing))
	}
	if def.Affinity != "" {
		out = append(out, fmt.Sprintf("组级亲和 %q 尚未实现，已忽略（渠道级亲和仍生效）", def.Affinity))
	}
	if len(def.Levels) > 0 || len(def.Match) > 0 || len(def.Weights) > 0 ||
		len(def.Fast) > 0 || len(def.Off) > 0 {
		out = append(out, "levels / match / weights / fast / off 尚未接入路由器，已忽略")
	}
	// manual 组用 pick 指定唯一成员；再让规则改顺序会与用户的显式选择冲突
	// （对齐 aether Group.Live：manual 组的规则只保存、不生效）。
	if def.Routing == "manual" && len(def.Rules) > 0 {
		out = append(out, "manual 组的 rules 不生效（pick 优先），已保存但不会改动候选顺序")
	}
	for _, m := range def.Members {
		if _, isGroup := nestedGroupRef(m); isGroup {
			// 嵌套组的问题由 nestedGroupWarnings 按"组是否存在"判定，
			// 这里不再一律报"不支持"。
			continue
		}
		if !strings.Contains(m, "/") {
			if _, ok := provider.Get(m); !ok {
				out = append(out, fmt.Sprintf("成员 %q 不是渠道模型，已忽略", m))
			}
		}
	}
	return out
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

// groupDefsByID 把当前全部组按 ID 建索引（页面与保存校验共用）。
func (g *Gateway) groupDefsByID() map[string]routingGroupDef {
	defs := g.groupDefsForView()
	out := make(map[string]routingGroupDef, len(defs))
	for _, def := range defs {
		out[def.ID] = def
	}
	return out
}

// knownGroupIDs 返回当前可引用的路由组 ID 集合（基线 + 覆盖 + 自动发现）。
func (g *Gateway) knownGroupIDs() map[string]bool {
	defs := g.groupDefsForView()
	out := make(map[string]bool, len(defs))
	for _, def := range defs {
		out[def.ID] = true
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

	// 嵌套成员只在这里校验存在性：真正的展开在解析时按当前路由表递归进行
	// （带环检测与最大深度），所以注册顺序无关紧要。
	known := make(map[string]bool, len(merged))
	for name := range merged {
		if !deleted[name] {
			known[name] = true
		}
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
			if pid, _, ok := strings.Cut(def.Pick, "/"); ok && pid != "group" {
				chain = moveProviderFirst(chain, pid)
			}
		}
		valid := make([]string, 0, len(chain))
		for _, entry := range chain {
			if gid, isGroup := nestedGroupRef(entry); isGroup {
				// 自引用在这里就挡掉：链里留着它只会让每次解析都记一条
				// warning，页面上的 nestedGroupWarnings 仍会提示用户。
				if known[gid] && gid != name {
					valid = append(valid, entry)
				}
				continue
			}
			if _, ok := provider.Get(entry); ok {
				valid = append(valid, entry)
			}
		}
		if len(valid) > 0 {
			g.models.SetVirtualGroupWithStrategy(name, valid, strategy)
			// manual 组不挂规则（对齐 aether Group.Live）：pick 已经是用户
			// 的显式选择，规则再改顺序会两边都不生效。
			if len(def.Rules) > 0 && def.Routing != "manual" {
				g.models.SetVirtualGroupRules(name, def.Rules)
			}
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
	for _, entry := range chain {
		if _, isGroup := nestedGroupRef(entry); isGroup {
			// 嵌套组按 group/<id> 原样回显：页面据此展示"组里套组"，
			// 而不是把它当成一个不存在的渠道吞掉。
			out = append(out, entry)
			continue
		}
		m, ok := provider.Get(entry)
		if !ok {
			continue
		}
		for _, mm := range m.Manifest().Models {
			up := mm.UpstreamModel
			if up == "" {
				up = mm.ID
			}
			out = append(out, entry+"/"+up)
		}
	}
	return out
}

// groupDefReady 判断组的成员里是否有可用账号；嵌套组递归判断（带环检测）。
// 只用于页面上的"就绪"标记，因此用 best-effort：环或缺失一律视为不就绪。
func groupDefReady(def routingGroupDef, byID map[string]routingGroupDef, byModel map[string]routingMember, seen map[string]bool) bool {
	if seen[def.ID] {
		return false
	}
	seen[def.ID] = true
	defer delete(seen, def.ID)
	for _, id := range def.Members {
		if info, ok := byModel[id]; ok {
			if !info.ProviderOff {
				return true
			}
			continue
		}
		if gid, isGroup := nestedGroupRef(id); isGroup {
			if inner, ok := byID[gid]; ok && groupDefReady(inner, byID, byModel, seen) {
				return true
			}
		}
	}
	return false
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
	// 嵌套成员的展示名与就绪状态都要看内层组，先建索引。
	defsByID := make(map[string]routingGroupDef, len(defs))
	for _, def := range defs {
		defsByID[def.ID] = def
	}
	knownIDs := make(map[string]bool, len(defs))
	for id := range defsByID {
		knownIDs[id] = true
	}

	groups := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		members := make([]string, 0, len(def.Members))
		infos := make([]routingMember, 0, len(def.Members))
		for _, id := range def.Members {
			members = append(members, id)
			if info, ok := byModel[id]; ok {
				infos = append(infos, info)
				continue
			}
			// 嵌套组成员不是某个渠道的模型：单独给一行展示信息，否则
			// 页面上只剩一个裸 ID。可用性由内层组成员递归判断。
			if gid, isGroup := nestedGroupRef(id); isGroup {
				name := gid
				if inner, ok := defsByID[gid]; ok && inner.Name != "" {
					name = inner.Name
				}
				infos = append(infos, routingMember{ID: id, Name: name})
			}
		}
		ready := groupDefReady(def, defsByID, byModel, map[string]bool{})
		routingForView := def.Routing
		if _, ok := groupStrategySupport(routingForView); !ok {
			// 旧配置里的 weight/free 等不再回显，避免页面继续展示未实现选项。
			routingForView = "order"
		}
		warnings := routingWarnings(def)
		warnings = append(warnings, nestedGroupWarnings(def.Members, def.ID, knownIDs)...)
		gm := map[string]any{
			"id": def.ID, "name": def.Name, "members": members, "memberInfo": infos,
			"routing": routingForView, "routingEffective": groupStrategyFor(def.Routing),
			"warnings": warnings, "ready": ready, "auto": def.Auto,
		}
		// rules 已接入组规则引擎，回显给页面（旧字段 weights/levels 等仍不回显）。
		if len(def.Rules) > 0 {
			gm["rules"] = def.Rules
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
