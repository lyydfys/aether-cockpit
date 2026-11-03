package gateway

// 路由组的「组规则」与「嵌套展开」。
//
// 设计依据（对标 aether internal/provider/grouprule.go 与 group.go 的
// membersFound）：一个组除了「按什么策略排序」，还需要「在什么条件下把某个
// 成员提到最前」。Aether 先实现三类条件——intent / effort / time——它们覆盖
// 绝大多数真实场景，且判定过程全是纯函数，便于单测：
//
//   - intent：复用 smart 策略背后的意图分类器。注意 Aether 的分类器回答的是
//     「这段提问最该交给哪个候选」（返回候选下标），而不是一个意图标签；
//     因此规则里的 intent 被解释为「分类器点名的候选属于这一类」，可写渠道
//     ID、上游模型名或显示名。分类器缺失 / 未配置 / 返回失败时条件不成立
//     （fail-open 到默认顺序）。
//   - effort：请求显式要求的 reasoning effort 达到或超过规则的档位；
//     "on" 表示请求处于任意推理模式。
//   - time：按 IANA 时区的时段（支持跨午夜）与星期过滤。时区名加载失败
//     视为「这个时钟不存在」，窗口一律不成立——宁可不命中，也不要在错的
//     时间点把请求发到贵渠道。
//
// 护栏：规则只负责重排候选，绝不新增或丢弃。applyGroupRules 出口用
// isPermutation 兜底，任何实现 bug 都退化为原序，契约与 applyRouteStrategy
// 完全一致。

import (
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// nestedGroupPrefix 是「组里套组」的成员前缀：chain / members 里写
	// group/<id>。group 本身不是渠道 ID，不会与真实渠道冲突。
	nestedGroupPrefix = "group/"
	// groupMaxNest 是嵌套展开的最大层数（对齐 aether 的 maxNest = 8）。
	// 超过就截断并记 warning：一个写坏的嵌套不该让请求解析失败。
	groupMaxNest = 8
	// groupRuleIntentMaxRunes 是 intent 描述的最大长度，防止配置里塞长文本。
	groupRuleIntentMaxRunes = 200
)

// groupEfforts 是 reasoning effort 从低到高的档位。顺序必须与
// model_catalog.go 的 effortRank 一致——那里是同一张表的数字化形式。
var groupEfforts = []string{"low", "medium", "high", "xhigh", "max"}

// GroupRule 是一条组规则：所有条件都成立时，把 Use 列出的成员按顺序提到
// 候选链首，其余成员保持原有相对顺序（fallback 链语义完整保留）。
type GroupRule struct {
	// Use 命中的成员（"provider/model"），按列出顺序前移；其余成员保持原序。
	Use []string `json:"use,omitempty"`
	// UseEffort 命中时要求请求改用的 reasoning effort。它是输出而不是条件：
	// 与 Use 配对，用来实现「这条规则选中的模型按这个档位思考」。
	UseEffort string `json:"useEffort,omitempty"`
	// Effort 条件：请求要求的推理档位 ≥ 该值；"on" 表示任意推理。
	Effort string `json:"effort,omitempty"`
	// Intent 条件：意图分类器点名的候选属于这一类（渠道 ID / 上游模型名 /
	// 显示名 / provider/model）。分类器不可用时该条件不成立。
	Intent string `json:"intent,omitempty"`
	// Time 条件：命中时段与星期。
	Time *TimeWindow `json:"time,omitempty"`
}

// TimeWindow 是「一天里的某个时段」：From 到 To，均为 HH:MM。
// To 早于 From 表示跨午夜（22:00–06:00）；两者相等表示全天。
// 跨午夜的窗口归属它开始的那一天：周五 22:00–06:00 也包含周六凌晨。
type TimeWindow struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Days 限定的星期：0=周日 … 6=周六（与 time.Weekday 一致）；
	// 7 作为周日的别名（ISO 写法）。空 = 每天。
	Days []int `json:"days,omitempty"`
	// Zone 是 IANA 时区名（如 Asia/Shanghai）。空 = 本机时区。
	// 名字加载失败视为「这个时钟不存在」，窗口一律不成立。
	Zone string `json:"zone,omitempty"`
}

// ruleZones 缓存已加载的时区。Holds 会在每个可能命中 time 规则的请求上被
// 调用，而 time.LoadLocation 每次都要读 tzdata。
var ruleZones sync.Map // name -> *time.Location

// ruleZone 解析时区名；空名取本机时区。
func ruleZone(name string) (*time.Location, bool) {
	if name == "" {
		return time.Local, true
	}
	if l, ok := ruleZones.Load(name); ok {
		return l.(*time.Location), true
	}
	l, err := time.LoadLocation(name)
	if err != nil {
		return nil, false
	}
	ruleZones.Store(name, l)
	return l, true
}

// ValidRuleZone 报告时区名是否可用（空名表示本机时钟，总是可用）。
// 保存规则时用它给出明确错误，而不是等到请求时才静默不命中。
func ValidRuleZone(name string) bool {
	_, ok := ruleZone(strings.TrimSpace(name))
	return ok
}

// clockMinutes 把 "HH:MM"（也接受 "H:MM"）读成「距午夜多少分钟」。
func clockMinutes(s string) (int, bool) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok || len(h) < 1 || len(h) > 2 || len(m) != 2 {
		return 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// clean 校验并归一化时间窗：时分为 "HH:MM"，星期去重排序，时区名合法。
// 覆盖全天且不限星期的窗口等于「无条件」，保存时直接拒绝——否则它会把
// 后面的规则全部短路，用户却看不出原因。
func (w *TimeWindow) clean() error {
	from, ok := clockMinutes(w.From)
	if !ok {
		return fmt.Errorf("时段起点 %q 不是 HH:MM（例如 09:00）", w.From)
	}
	to, ok := clockMinutes(w.To)
	if !ok {
		return fmt.Errorf("时段终点 %q 不是 HH:MM（例如 18:00）", w.To)
	}
	w.From = fmt.Sprintf("%02d:%02d", from/60, from%60)
	w.To = fmt.Sprintf("%02d:%02d", to/60, to%60)

	var days []int
	for _, d := range w.Days {
		if d < 0 || d > 7 {
			return fmt.Errorf("星期 %d 超出范围（0=周日 … 6=周六，7 为周日别名）", d)
		}
		nd := d % 7
		if !slices.Contains(days, nd) {
			days = append(days, nd)
		}
	}
	slices.Sort(days)
	if len(days) == 7 {
		days = nil // 七天等于不限星期
	}
	w.Days = days

	w.Zone = strings.TrimSpace(w.Zone)
	if !ValidRuleZone(w.Zone) {
		return fmt.Errorf("时区 %q 不是可用的 IANA 时区名（例如 Asia/Shanghai）", w.Zone)
	}
	if from == to && len(days) == 0 {
		return fmt.Errorf("时段覆盖全天且不限星期，等于无条件；请给出具体时段或星期")
	}
	return nil
}

// Holds 报告 at 是否落在窗口内。零值时间不在任何窗口里。
func (w TimeWindow) Holds(at time.Time) bool {
	if at.IsZero() {
		return false
	}
	from, ok1 := clockMinutes(w.From)
	to, ok2 := clockMinutes(w.To)
	loc, ok3 := ruleZone(strings.TrimSpace(w.Zone))
	if !ok1 || !ok2 || !ok3 {
		return false
	}
	at = at.In(loc)
	m, day := at.Hour()*60+at.Minute(), int(at.Weekday())
	switch {
	case from == to: // 全天
	case from < to:
		if m < from || m >= to {
			return false
		}
	default: // 跨午夜
		if m >= to && m < from {
			return false
		}
		if m < to {
			day = (day + 6) % 7 // 窗口从前一天开始
		}
	}
	if len(w.Days) == 0 {
		return true
	}
	for _, d := range w.Days {
		if d%7 == day {
			return true
		}
	}
	return false
}

// nestedGroupRef 判断一个成员/链项是否是嵌套组引用，返回组 ID。
func nestedGroupRef(entry string) (string, bool) {
	s := strings.TrimSpace(entry)
	if !strings.HasPrefix(s, nestedGroupPrefix) {
		return "", false
	}
	id := strings.TrimSpace(strings.TrimPrefix(s, nestedGroupPrefix))
	if id == "" {
		return "", false
	}
	return id, true
}

// groupChainSource 是嵌套展开需要的最小只读视图。展开本身是纯函数：只读
// Chains / DefaultSeat，不碰路由器的锁与状态；内层组自己的排序策略通过
// Segment 回调交给调用方（纯展开时传 nil）。
type groupChainSource struct {
	// Chains 是「组 ID -> 候选链」，链项可以是渠道 ID 或 group/<id>。
	Chains map[string][]string
	// DefaultSeat 把渠道 ID 变成默认绑定；nil 表示只展开嵌套结构。
	DefaultSeat func(providerID string) (modelBinding, bool)
	// Segment 可选：某个嵌套组展开出的候选先交给它（调用方在这里套用该组
	// 自己的排序策略）。nil = 纯展开。
	Segment func(group string, seats []modelBinding) []modelBinding
	// MaxDepth 覆盖默认的最大嵌套层数；<=0 用 groupMaxNest。
	MaxDepth int
}

// expandGroupChain 把 group 的候选链展开成绑定列表：普通成员取默认绑定，
// group/<id> 递归展开。展开顺序即外层声明顺序；重复候选按
// provider + upstream 去重（保留先出现的位置）。
//
// 防环与限深：同一分支上再次遇到已展开的组、嵌套超过 MaxDepth 层、或引用了
// 未注册的组时，保留外层其余成员的顺序、跳过该引用并返回一条 warning——
// 绝不 panic，也绝不因为一个坏引用丢掉整条链。
func expandGroupChain(src groupChainSource, group string) ([]modelBinding, []string) {
	maxDepth := src.MaxDepth
	if maxDepth <= 0 {
		maxDepth = groupMaxNest
	}
	// seen 是「当前分支已经展开过的组」，出栈时删除：兄弟分支里重复引用
	// 同一个组不算环，去重交给 appendBindingUnique。
	seen := map[string]bool{group: true}
	var walk func(g string, depth int) ([]modelBinding, []string)
	walk = func(g string, depth int) ([]modelBinding, []string) {
		var out []modelBinding
		var warns []string
		for _, entry := range src.Chains[g] {
			gid, isGroup := nestedGroupRef(entry)
			if !isGroup {
				if src.DefaultSeat == nil {
					continue
				}
				if b, ok := src.DefaultSeat(strings.TrimSpace(entry)); ok {
					out = appendBindingUnique(out, b)
				}
				continue
			}
			if seen[gid] {
				warns = append(warns, fmt.Sprintf("路由组 %q 的成员 %q 成环，已跳过", g, entry))
				continue
			}
			if _, ok := src.Chains[gid]; !ok {
				warns = append(warns, fmt.Sprintf("路由组 %q 的成员 %q 未注册或已删除，已跳过", g, entry))
				continue
			}
			if depth+1 > maxDepth {
				warns = append(warns, fmt.Sprintf("路由组 %q 的成员 %q 超过 %d 层嵌套，已跳过", g, entry, maxDepth))
				continue
			}
			seen[gid] = true
			sub, w := walk(gid, depth+1)
			delete(seen, gid)
			warns = append(warns, w...)
			if src.Segment != nil && len(sub) > 0 {
				sub = src.Segment(gid, sub)
			}
			for _, b := range sub {
				out = appendBindingUnique(out, b)
			}
		}
		return out, warns
	}
	return walk(group, 0)
}

// groupWarnOnce 保证同一条嵌套配置错误在进程内只记一次日志：解析路径每个
// 请求都会展开组，坏配置不该把日志刷爆（页面上的 warnings 仍每次都提示）。
var groupWarnOnce sync.Map

// logGroupWarnings 把展开期的配置问题写成日志（去重后）。
func logGroupWarnings(warns []string) {
	for _, w := range warns {
		if _, loaded := groupWarnOnce.LoadOrStore(w, true); loaded {
			continue
		}
		log.Printf("[WARN] %s", w)
	}
}

// nestedGroupWarnings 返回页面要展示的嵌套成员问题：引用了不存在的组、
// 指向组自身、或 group/ 后面忘了写组 ID。解析期的深层环由 expandGroupChain
// 记 warning。
func nestedGroupWarnings(members []string, self string, known map[string]bool) []string {
	var out []string
	for _, m := range members {
		gid, ok := nestedGroupRef(m)
		if !ok {
			if strings.HasPrefix(strings.TrimSpace(m), nestedGroupPrefix) {
				out = append(out, fmt.Sprintf("成员 %q 缺少组 ID（应写成 group/<id>），已忽略", m))
			}
			continue
		}
		switch {
		case gid == self:
			out = append(out, fmt.Sprintf("成员 %q 指向组自身（成环），已忽略", m))
		case !known[gid]:
			out = append(out, fmt.Sprintf("成员 %q 引用的路由组不存在，已忽略", m))
		}
	}
	return out
}

// missingNestedGroups 返回保存时必须拒绝的嵌套成员（不存在 / 指向自身 /
// group/ 之后缺 ID）。
func missingNestedGroups(members []string, self string, known map[string]bool) []string {
	var out []string
	for _, m := range members {
		if !strings.HasPrefix(strings.TrimSpace(m), nestedGroupPrefix) {
			continue
		}
		gid, ok := nestedGroupRef(m)
		if !ok || gid == self || !known[gid] {
			out = append(out, m)
		}
	}
	return out
}

// expandMembersForRules 把成员里的嵌套引用递归展开成具体成员
// （"provider/model"），供组规则校验 use 是否属于可路由候选——组规则命中后
// 重排的是展开后的候选，所以内层成员也是合法目标。环与缺失组直接跳过：
// 这里只用于保存校验，真出问题的配置由 prepareGroupDef 更早拒绝。
func expandMembersForRules(members []string, defs map[string]routingGroupDef, seen map[string]bool) []string {
	var out []string
	for _, m := range members {
		gid, isGroup := nestedGroupRef(m)
		if !isGroup {
			if m != "" && !containsString(out, m) {
				out = append(out, m)
			}
			continue
		}
		if seen[gid] {
			continue
		}
		inner, ok := defs[gid]
		if !ok {
			continue
		}
		seen[gid] = true
		for _, x := range expandMembersForRules(inner.Members, defs, seen) {
			if !containsString(out, x) {
				out = append(out, x)
			}
		}
		delete(seen, gid)
	}
	return out
}

// rulesNeedIntent 报告规则里是否有 intent 条件（决定要不要注入分类器）。
func rulesNeedIntent(rules []GroupRule) bool {
	for _, r := range rules {
		if r.Intent != "" {
			return true
		}
	}
	return false
}

// cleanGroupRules 校验并归一化一组组规则。保存路径调用：规则写错必须在保存
// 时拒绝，而不是存下去永远不生效——与 routingWarnings 的「不静默降级」
// 原则一致。
func cleanGroupRules(rules []GroupRule, members []string) ([]GroupRule, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	known := make(map[string]bool, len(members))
	for _, m := range members {
		known[strings.TrimSpace(m)] = true
	}
	out := make([]GroupRule, 0, len(rules))
	for i, r := range rules {
		n := i + 1
		var uses []string
		for _, u := range r.Use {
			u = strings.TrimSpace(u)
			if u == "" || slices.Contains(uses, u) {
				continue
			}
			if _, isGroup := nestedGroupRef(u); isGroup {
				// 规则层的候选下标在嵌套展开后已失去「组」这一层信息，
				// 直接拒绝比假装支持（永远不命中）更诚实。
				return nil, fmt.Errorf("规则 %d：use 暂不支持指向嵌套组（%q），请指向具体模型", n, u)
			}
			if !known[u] {
				return nil, fmt.Errorf("规则 %d：%q 不是该组的成员", n, u)
			}
			uses = append(uses, u)
		}
		if len(uses) == 0 {
			return nil, fmt.Errorf("规则 %d：缺少 use（命中时要把哪个成员提到最前）", n)
		}
		r.Use = uses

		r.UseEffort = normalizeEffortRequest(r.UseEffort)
		if r.UseEffort != "" {
			if _, ok := effortRank[r.UseEffort]; !ok {
				return nil, fmt.Errorf("规则 %d：useEffort %q 不是有效档位（%s）",
					n, r.UseEffort, strings.Join(groupEfforts, " / "))
			}
		}

		r.Effort = strings.ToLower(strings.TrimSpace(r.Effort))
		if r.Effort != "" && r.Effort != "on" {
			if _, ok := effortRank[r.Effort]; !ok {
				return nil, fmt.Errorf("规则 %d：effort %q 不是有效条件（on 或 %s）",
					n, r.Effort, strings.Join(groupEfforts, " / "))
			}
		}

		r.Intent = strings.Join(strings.Fields(r.Intent), " ")
		if len([]rune(r.Intent)) > groupRuleIntentMaxRunes {
			return nil, fmt.Errorf("规则 %d：intent 最多 %d 个字符", n, groupRuleIntentMaxRunes)
		}

		if r.Time != nil {
			w := *r.Time
			if err := w.clean(); err != nil {
				return nil, fmt.Errorf("规则 %d：%w", n, err)
			}
			r.Time = &w
		}
		if r.Effort == "" && r.Intent == "" && r.Time == nil {
			return nil, fmt.Errorf("规则 %d：至少要有一个条件（effort / intent / time）", n)
		}
		out = append(out, r)
	}
	return out, nil
}

// effortConditionHolds 判断「请求要求的档位」是否满足规则的 effort 条件。
// 写错的档位一律不匹配（不猜用户想要什么），"on" 只要求处于推理模式。
func effortConditionHolds(want, got string) bool {
	w := strings.ToLower(strings.TrimSpace(want))
	if w == "" {
		return true
	}
	g := normalizeEffortRequest(got)
	if w == "on" {
		return g != ""
	}
	wi, ok := effortRank[w]
	if !ok {
		return false
	}
	if g == "" {
		return false
	}
	gi, ok := effortRank[g]
	return ok && gi >= wi
}

// bindingMatchesMember 报告绑定是否就是规则 use 写的那个成员。
// use 形如 "provider/model"；上游模型名本身可能带斜杠（OpenRouter），
// 所以只在第一个斜杠处切分。
func bindingMatchesMember(b modelBinding, use string) bool {
	use = strings.TrimSpace(use)
	pid, model, ok := strings.Cut(use, "/")
	if !ok || pid == "" || model == "" {
		return false
	}
	if b.ProviderID != pid {
		return false
	}
	if strings.EqualFold(b.UpstreamModel, model) || strings.EqualFold(b.DisplayName, model) {
		return true
	}
	return strings.EqualFold(b.ProviderID+"/"+b.UpstreamModel, use)
}

// bindingMatchesIntent 报告绑定是否属于 intent 点名的那一类。
// Aether 的意图分类器返回的是「最该交给哪个候选」，所以 intent 比较的是
// 候选的身份（渠道 ID / 上游模型名 / 显示名 / provider/model）。
func bindingMatchesIntent(b modelBinding, want string) bool {
	w := strings.ToLower(strings.TrimSpace(want))
	if w == "" || b.ProviderID == "" {
		return false
	}
	return w == strings.ToLower(b.ProviderID) ||
		w == strings.ToLower(b.UpstreamModel) ||
		w == strings.ToLower(b.DisplayName) ||
		w == strings.ToLower(b.ProviderID+"/"+b.UpstreamModel)
}

// moveMembersFirst 把 uses 点名的成员按 uses 顺序移到最前，其余保持原序。
// 不认识的成员直接跳过：规则绝不引入候选。
func moveMembersFirst(seats []modelBinding, uses []string) []modelBinding {
	if len(uses) == 0 {
		return seats
	}
	picked := make([]bool, len(seats))
	out := make([]modelBinding, 0, len(seats))
	for _, u := range uses {
		for i, b := range seats {
			if picked[i] || !bindingMatchesMember(b, u) {
				continue
			}
			picked[i] = true
			out = append(out, b)
			break
		}
	}
	for i, b := range seats {
		if !picked[i] {
			out = append(out, b)
		}
	}
	return out
}

// ruleNow 是规则 time 条件使用的时钟；RouteContext.Now 供测试注入。
func (ctx *RouteContext) ruleNow() time.Time {
	if ctx != nil && !ctx.Now.IsZero() {
		return ctx.Now
	}
	return time.Now()
}

// pickRuleIntent 把「分类器点名的候选」定格到 ctx 上，每个请求只问一次。
//
// 为什么必须定格而不是每次按当前下标取：分类器输出的是候选下标，候选链一旦
// 被 rotate / smart 重排，同一个下标就指向别的成员。applyGroupStrategyAndRules
// 因此在策略重排之前调用本方法；独立调用 applyGroupRules 的单测则退化为
// 按传入候选集问一次，语义一致。
func (ctx *RouteContext) pickRuleIntent(seats []modelBinding) {
	if ctx == nil || ctx.intentPicked {
		return
	}
	ctx.intentPicked = true
	if ctx.Intent == nil || strings.TrimSpace(ctx.Text) == "" || len(seats) == 0 {
		return
	}
	idx := ctx.Intent(ctx.Group, ctx.SessionKey, seats, ctx.Text)
	if idx < 0 || idx >= len(seats) {
		return
	}
	ctx.intentPick = seats[idx]
}

// matches 报告一条规则的全部条件是否成立（AND 语义）。
// 没有任何条件的规则一律不命中：旧配置里可能残留空规则，不能让它变成
// 「无条件改写顺序」。
func (r GroupRule) matches(ctx *RouteContext) bool {
	if ctx == nil {
		return false
	}
	if r.Effort == "" && r.Intent == "" && r.Time == nil {
		return false
	}
	if !effortConditionHolds(r.Effort, ctx.EffortIn) {
		return false
	}
	if r.Intent != "" && !bindingMatchesIntent(ctx.intentPick, r.Intent) {
		return false
	}
	if r.Time != nil && !r.Time.Holds(ctx.ruleNow()) {
		return false
	}
	return true
}

// applyGroupRules 在排序策略之后套用组规则。
//
// 返回值第二项是命中的规则序号（1 起；0 = 没有规则命中）。
// 规则按声明顺序取第一条命中的：命中后把 Use 提到链首，其余保持原序，因此
// 结果必然是输入候选的一个排列；出口再用 isPermutation 兜底一次，契约与
// applyRouteStrategy 完全一致——规则写错最多让顺序退化，绝不会引入或丢掉
// 候选（fail-open）。
func applyGroupRules(ctx *RouteContext, rules []GroupRule, seats []modelBinding) ([]modelBinding, int) {
	if ctx == nil || len(rules) == 0 || len(seats) < 2 {
		return seats, 0
	}
	// 生产路径已在策略重排前定格意图候选；这里是独立调用时的兜底。
	ctx.pickRuleIntent(seats)

	original := make([]modelBinding, len(seats))
	copy(original, seats)

	for i := range rules {
		rule := rules[i]
		if !rule.matches(ctx) {
			continue
		}
		if rule.UseEffort != "" {
			ctx.EffortOut = normalizeEffortRequest(rule.UseEffort)
		}
		out := moveMembersFirst(seats, rule.Use)
		if !isPermutation(original, out) {
			log.Printf("[WARN] 路由组 %q 的规则 %d 返回结果不是候选集的排列，退回原序", ctx.Group, i+1)
			return seats, 0
		}
		return out, i + 1
	}
	return seats, 0
}

// applyGroupStrategyAndRules 是虚拟组的完整路由管线：先按策略排序，再套组
// 规则。调用方必须持有 r.mu.RLock。
//
// 内层组（group/<id>）的候选在 groupSeatsLocked 里已各自套过自己的策略；
// 外层策略作用于整条展开链，这就是「内层组没写 routing 时继承外层 routing」
// （内层策略为 priority 时是空操作，对齐 aether Group.Within）。
func (r *ModelRouter) applyGroupStrategyAndRules(group string, seats []modelBinding, text, sessionKey, effortIn string, allowIntent bool) ([]modelBinding, RouteOutcome) {
	ctx := r.groupRouteContextLocked(group, text, sessionKey, allowIntent)
	ctx.EffortIn = normalizeEffortRequest(effortIn)
	rules := r.virtualRules[group]
	// 意图必须在策略重排之前定格：分类器给的是候选下标。
	if len(rules) > 0 && rulesNeedIntent(rules) {
		ctx.pickRuleIntent(seats)
	}
	strategy := r.virtualStrategy[group]
	if strategy == "" {
		strategy = GroupStrategyPriority
	}
	out := applyRouteStrategy(strategy, ctx, seats)
	out, hit := applyGroupRules(ctx, rules, out)
	return out, RouteOutcome{EffortOut: ctx.EffortOut, RuleHit: hit}
}
