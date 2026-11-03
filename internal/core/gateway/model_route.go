package gateway

import (
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// modelBinding 是一个模型到"渠道 + 上游模型名"的映射。
type modelBinding struct {
	ProviderID    string
	UpstreamModel string
	DisplayName   string
	// Priority 越小越优先。同一虚拟组内由用户拖拽排序决定。
	Priority int
}

// ModelRouter 负责把下游传来的模型名解析成有序的渠道候选列表。
//
// 支持三种寻址方式：
//  1. 精确渠道：workbuddy-cn/claude-4-sonnet
//  2. 虚拟聚合：auto/claude-sonnet  → 按配置的优先级链依次尝试
//  3. 裸模型名：claude-4-sonnet     → 所有提供该模型的渠道按注册顺序
type ModelRouter struct {
	mu sync.RWMutex
	// exact 完整 ID -> 绑定
	exact map[string]modelBinding
	// bare 裸模型名 -> 候选列表
	bare map[string][]modelBinding
	// virtual 虚拟模型名 -> 渠道优先级链
	virtual map[string][]string
	// virtualStrategy 虚拟组的排序策略名（priority | rotate | pace，见 routeStrategies）。
	virtualStrategy map[string]string
	// virtualRules 虚拟组 -> 组规则（intent / effort / time），见 group_rule.go。
	// 规则在策略排序之后套用，只重排候选、不改变候选集合。
	virtualRules map[string][]GroupRule
	// rotateCounters rotate 策略的轮转计数器，按组名独立。
	rotateCounters map[string]*atomic.Uint64
	// quotaLookup 渠道额度查询函数（由网关把账号池接进来，见 SetQuotaLookup）。
	// nil = 无额度数据，pace 策略把所有渠道视为同权、保持原序。
	quotaLookup atomic.Pointer[QuotaLookup]
	// intentFn 意图分类函数（由网关把意图分类器接进来，见 SetIntentClassifier）。
	// nil = 不做意图分类，smart 策略保持原序。
	intentFn atomic.Pointer[IntentClassifyFunc]
	// latencyLookup 渠道时延查询函数（由网关把账号池接进来，见 SetLatencyLookup）。
	// nil = 无时延样本，fast 策略保持原序。
	latencyLookup atomic.Pointer[LatencyLookup]
	// enabledFilter 由网关注入：返回 false 的渠道不参与解析（渠道页的开关）。
	enabledFilter atomic.Pointer[func(string) bool]
	// fallbackModels 无任何匹配时，用于宽松兜底的渠道（仍用原模型名请求上游）
	chatProviders []string
	// modelSource 由网关注入：返回渠道当前模型清单（静态 + 上游实时）。
	// nil 时直接用 Manifest().Models。
	modelSource atomic.Pointer[func(string) []ir.ModelMeta]
	// alias 是归一化/规范模型名 -> 跨渠道绑定链（同一模型的不同写法自动成链）。
	alias map[string][]modelBinding
	// redirects 是用户配置的重定向：归一化虚拟名 -> 渠道+上游模型候选链。
	redirects map[string][]modelBinding
	// redirectRaw 保存原始重定向配置，Rebuild 时按当前渠道注册顺序重算。
	redirectRaw map[string]map[string]string
	// aliaser 由网关注入：返回某渠道模型的规范别名（models.dev canonical/显示名）。
	aliaser atomic.Pointer[func(string, string) []string]
	// readyLookup 由网关注入：渠道当前是否有可用账号。解析时优先有额度的渠道。
	readyLookup atomic.Pointer[func(string) bool]
}

// QuotaLookup 查询某渠道当前"最可用账号"的额度视图：
// remaining 剩余额度、resetAt 额度重置时刻（Unix 秒，0 表示未知）。
// 渠道无账号或无额度数据时 ok=false。
type QuotaLookup func(providerID string) (remaining int, resetAt int64, ok bool)

// RouteStrategy 把候选渠道链排序。实现必须是纯函数：只重排传入的切片并返回新排列，
// 绝不修改外部状态（rotate 的计数器由框架持有并通过 ctx 传入）。
//
// 为什么要求纯函数：路由是每个请求的必经之路，策略一旦自己记账（计数器、
// 粘性、熔断……）就会在并发与故障场景下互相踩踏；把"排序"与"状态"拆开后，
// 策略本身可以随意 panic / 被 recover / 被替换单测，而网关侧的状态机不受影响。
type RouteStrategy func(ctx *RouteContext, seats []modelBinding) []modelBinding

// RouteContext 是策略可用的只读环境。
type RouteContext struct {
	// Group 虚拟组名。Rotate() 返回该组的下一个轮转偏移（框架持有计数器，线程安全）。
	Group string
	// Text 当前请求最后一轮的用户文本，smart 策略把它交给意图分类器。
	Text string
	// SessionKey 跨轮稳定的会话指纹，意图分类器用它做"短追问继承"。
	SessionKey string
	// Rotate 返回 [0, n) 内的轮转偏移；n<=0 返回 0。
	Rotate func(n int) int
	// Quota 返回某渠道当前"最可用账号"的额度视图（剩余额度与重置时间）。
	// 渠道无账号/无额度数据时 ok=false；未注入时为 nil。
	Quota QuotaLookup
	// Intent 意图分类函数（仅 smart 策略使用）。nil = 不分类（保持原序）。
	//
	// 防递归约定：网关自身发出的分类调用（X-Aether-Intent 头）不会带上
	// Intent——否则分类模型的请求又会触发一次分类，无限套娃。框架只在
	// "下游真实请求 + smart 策略"的路径上注入。
	Intent IntentClassifyFunc
	// Latency 渠道时延查询（仅 fast 策略使用）。nil = 无样本。
	Latency LatencyLookup
	// EffortIn 是请求显式要求的 reasoning effort（已归一化，空 = 未要求）。
	// 组规则的 effort 条件读它；策略不读。
	EffortIn string
	// EffortOut 是组规则命中后要求请求改用的 reasoning effort（UseEffort）。
	// 规则层只写这个字段，由 dispatch 在拿到候选链后写回 req.ReasoningEffort——
	// 路由表不应该反过来改请求，否则策略与规则都能偷偷改请求语义。
	EffortOut string
	// 组规则的其它条件用这些信号（都由 dispatch 从请求里填，路由本身不解析协议）：
	//   - Tokens：输入文本的 rune 数（大致等价于"上下文有多大"）；
	//   - Images：请求是否带图片；
	//   - Agent：客户端标识（client_agent）；
	//   - Folder：工作目录。
	Tokens int
	Images bool
	Agent  string
	Folder string
	// Now 是规则 time 条件使用的时钟；零值 = time.Now()（测试注入用）。
	Now time.Time
	// intentPick / intentPicked 是规则层定格下来的「分类器点名的候选」。
	//
	// 为什么要在策略重排之前定格：分类器输出的是候选下标，rotate / smart
	// 一旦把链重排，同一个下标就指向别的成员。这里存绑定而不是下标。
	intentPick   modelBinding
	intentPicked bool
}

// RouteOutcome 是一次解析的附加输出：它不参与候选顺序，只描述「这次解析
// 决定了什么」。dispatch 拿到候选链后可以按需写回请求（例如组规则的
// UseEffort），trace 也可以记下命中的规则序号。
type RouteOutcome struct {
	// EffortOut 是组规则命中时要求改用的 reasoning effort（已归一化）；
	// 空表示规则没有要求，保持客户端原值。
	EffortOut string
	// RuleHit 是命中的组规则序号（1 起；0 = 没有规则命中）。
	RuleHit int
}

// LatencyLookup 返回渠道的时延估计（首字节毫秒 + 吞吐 token/s）。
type LatencyLookup func(providerID string) (ttftMS, tps float64, ok bool)

// 虚拟组支持的策略。命名对齐同类网关的惯例：
// priority = 固定优先级链（等价于别处的 order）；rotate = 轮转均摊；
// pace = 按"距重置每小时可消耗额度"降序，优先消耗临期积分；
// weighted = 按剩余额度加权（账号池 pool.Select 的既有行为，无需在此处理）；
// smart = 意图路由：用小分类模型按提问内容把"最强"渠道提到链首。
const (
	GroupStrategyPriority = "priority"
	GroupStrategyRotate   = "rotate"
	GroupStrategyPace     = "pace"
	// GroupStrategyUsage 按"剩余额度最多"排序（不看到期时间），
	// 与 pace 的区别见 strategyUsage 的注释。
	GroupStrategyUsage = "usage"
	GroupStrategySmart = "smart"
	// GroupStrategyFast 按实测时延排序：TTFT 升序，并列看吞吐。
	GroupStrategyFast = "fast"
)

// routeStrategies 策略注册表。init 阶段静态注册；测试可用
// registerRouteStrategy 注入临时策略（用完删除）。
var routeStrategies = map[string]RouteStrategy{}

// registerRouteStrategy 注册一个路由策略，重复注册视为编程错误（panic）。
func registerRouteStrategy(name string, fn RouteStrategy) {
	if name == "" || fn == nil {
		panic("路由策略名与实现均不能为空")
	}
	if _, dup := routeStrategies[name]; dup {
		panic("路由策略重复注册: " + name)
	}
	routeStrategies[name] = fn
}

func init() {
	registerRouteStrategy(GroupStrategyPriority, strategyPriority)
	registerRouteStrategy(GroupStrategyRotate, strategyRotate)
	registerRouteStrategy(GroupStrategyPace, strategyPace)
	registerRouteStrategy(GroupStrategyUsage, strategyUsage)
	registerRouteStrategy(GroupStrategySmart, strategySmart)
	registerRouteStrategy(GroupStrategyFast, strategyFast)
}

// NewModelRouter 创建路由表。
func NewModelRouter() *ModelRouter {
	return &ModelRouter{
		exact:           make(map[string]modelBinding, 64),
		bare:            make(map[string][]modelBinding, 64),
		virtual:         make(map[string][]string, 8),
		virtualStrategy: make(map[string]string, 8),
		virtualRules:    make(map[string][]GroupRule, 8),
		rotateCounters:  make(map[string]*atomic.Uint64, 8),
		alias:           make(map[string][]modelBinding, 128),
		redirects:       make(map[string][]modelBinding, 8),
		redirectRaw:     make(map[string]map[string]string, 8),
	}
}

// SetModelRedirects 设置「虚拟模型名 -> 渠道 -> 上游模型名」的重定向表。
// 目标顺序按渠道注册顺序；解析时仍会把有可用账号的渠道排到前面。
func (r *ModelRouter) SetModelRedirects(in map[string]map[string]string) {
	r.mu.Lock()
	r.redirectRaw = cloneModelRedirects(in)
	r.rebuildRedirectsLocked()
	r.mu.Unlock()
}

// rebuildRedirectsLocked 按当前渠道注册顺序重建重定向候选链。
// 调用方必须持有 r.mu 写锁。
func (r *ModelRouter) rebuildRedirectsLocked() {
	out := make(map[string][]modelBinding, len(r.redirectRaw))
	for alias, targets := range r.redirectRaw {
		list := make([]modelBinding, 0, len(targets))
		for idx, p := range provider.All() {
			pid := p.Manifest().ID
			upstream, ok := targets[pid]
			if !ok {
				continue
			}
			// 用户可能写公开模型名（如 Qoder 的 DeepSeek-Flash），也可能写
			// 上游 key（dfmodel）。能在目录里对上就换成真正的上游名，避免
			// 重定向配置看起来生效、实际把显示名发给上游。
			list = append(list, modelBinding{
				ProviderID:    pid,
				UpstreamModel: r.resolveRedirectUpstream(pid, upstream),
				DisplayName:   alias,
				Priority:      idx,
			})
		}
		if len(list) > 0 {
			out[normalizeModelKey(alias)] = list
		}
	}
	r.redirects = out
}

// resolveRedirectUpstream 把重定向目标折成渠道真正发送的上游模型名。
// 优先按目录里的公开 ID / 显示名 / 已有上游名匹配；对不上则原样返回，
// 支持用户直接填上游 key。
func (r *ModelRouter) resolveRedirectUpstream(pid, target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if i := strings.LastIndex(target, "/"); i > 0 {
		if target[:i] == pid {
			target = target[i+1:]
		} else {
			// OpenRouter 等上游的模型名本身就带斜杠；没有本渠道前缀时
			// 不做目录匹配，原样发送，避免误撞成本渠道的同名模型。
			return target
		}
	}
	key := normalizeModelKey(target)
	if src := r.modelSource.Load(); src != nil && *src != nil {
		for _, m := range (*src)(pid) {
			if normalizeModelKey(m.ID) != key &&
				normalizeModelKey(m.UpstreamModel) != key &&
				normalizeModelKey(m.DisplayName) != key {
				continue
			}
			if up := strings.TrimSpace(m.UpstreamModel); up != "" {
				return up
			}
			return strings.TrimPrefix(m.ID, pid+"/")
		}
	}
	return target
}

// HasRedirect 报告某个名字是否是已配置的重定向虚拟名。
func (r *ModelRouter) HasRedirect(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.redirects[normalizeModelKey(name)]
	return ok
}

// SetModelAliaser 注入规范别名（models.dev canonical + 显示名）。
func (r *ModelRouter) SetModelAliaser(fn func(string, string) []string) {
	if fn == nil {
		r.aliaser.Store(nil)
		return
	}
	r.aliaser.Store(&fn)
}

// SetProviderReady 注入渠道可用性，用于解析时优先有可用账号的渠道。
func (r *ModelRouter) SetProviderReady(fn func(string) bool) {
	if fn == nil {
		r.readyLookup.Store(nil)
		return
	}
	r.readyLookup.Store(&fn)
}

// SetModelSource 注入实时模型目录。传 nil 恢复只读 Manifest。
func (r *ModelRouter) SetModelSource(fn func(string) []ir.ModelMeta) {
	if fn == nil {
		r.modelSource.Store(nil)
		return
	}
	r.modelSource.Store(&fn)
}

// Rebuild 根据当前注册的渠道重建索引。渠道变化时调用。
func (r *ModelRouter) Rebuild() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.exact = make(map[string]modelBinding, 64)
	r.bare = make(map[string][]modelBinding, 64)
	r.alias = make(map[string][]modelBinding, 128)
	r.chatProviders = r.chatProviders[:0]
	r.rebuildRedirectsLocked()

	for idx, p := range provider.All() {
		m := p.Manifest()
		if _, ok := provider.AsChat(p); !ok {
			continue
		}
		r.chatProviders = append(r.chatProviders, m.ID)

		models := m.Models
		if src := r.modelSource.Load(); src != nil {
			if effective := (*src)(m.ID); effective != nil {
				// 目录模式（merge / fallback / allowlist）已在模型来源里
				// 处理；路由层不再自行合并，否则 fallback 与 allowlist
				// 会被这里的 merge 抵消。
				models = effective
			}
		}
		for _, mm := range models {
			b := modelBinding{
				ProviderID:    m.ID,
				UpstreamModel: mm.UpstreamModel,
				DisplayName:   mm.DisplayName,
				Priority:      idx,
			}
			if mm.UpstreamModel == "" {
				b.UpstreamModel = mm.ID
			}
			// 完整 ID：优先用 manifest 里写的，其次自动拼 providerID/model。
			fullID := mm.ID
			if !strings.Contains(fullID, "/") {
				fullID = m.ID + "/" + mm.ID
			}
			r.exact[fullID] = b

			// 裸模型名。
			bare := mm.ID
			if i := strings.LastIndex(bare, "/"); i >= 0 {
				bare = bare[i+1:]
			}
			r.bare[bare] = append(r.bare[bare], b)

			// 归一化/规范名：大小写、连字符、渠道前缀，以及 models.dev 的
			// canonical_model_id 都归到同一个键，跨渠道自动成链。
			keys := []string{normalizeModelKey(bare)}
			if fn := r.aliaser.Load(); fn != nil && *fn != nil {
				keys = append(keys, (*fn)(m.ID, bare)...)
			}
			seenKey := map[string]bool{}
			for _, k := range keys {
				if k == "" || seenKey[k] {
					continue
				}
				seenKey[k] = true
				r.alias[k] = appendBindingUnique(r.alias[k], b)
			}
		}
	}

	// 保持优先级稳定。
	for k := range r.bare {
		list := r.bare[k]
		sort.SliceStable(list, func(i, j int) bool { return list[i].Priority < list[j].Priority })
		r.bare[k] = list
	}
}

// SetVirtualGroup 定义一个虚拟模型的渠道优先级链（默认 priority 策略）。
func (r *ModelRouter) SetVirtualGroup(name string, providerChain []string) {
	r.SetVirtualGroupWithStrategy(name, providerChain, GroupStrategyPriority)
}

// SetVirtualGroupWithStrategy 定义虚拟组并指定排序策略。
//
// strategy 取值（见 routeStrategies 注册表）：
//   - priority：按配置顺序组成 fallback 链（默认）；
//   - rotate：每次请求把链的起点轮转一位，把负载均匀摊到各渠道上，
//     失败时依然沿链 fallback；
//   - pace：按"距重置每小时可消耗额度"降序，优先消耗即将清零的额度；
//   - smart：意图路由——按用户提问内容把分类模型选出的"最强"渠道提到
//     链首，其余保持相对顺序。需同时配置 intent_classify_model，否则退化为
//     priority。
//
// 未注册的策略名回落 priority（记日志），配置写错不至于让组整体失效。
func (r *ModelRouter) SetVirtualGroupWithStrategy(name string, providerChain []string, strategy string) {
	if strategy == "" {
		strategy = GroupStrategyPriority
	} else if _, ok := routeStrategies[strategy]; !ok {
		log.Printf("[WARN] 虚拟组 %q 的策略 %q 未注册，回落到 %s", name, strategy, GroupStrategyPriority)
		strategy = GroupStrategyPriority
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.virtual[name] = providerChain
	r.virtualStrategy[name] = strategy
	if _, ok := r.rotateCounters[name]; !ok {
		r.rotateCounters[name] = &atomic.Uint64{}
	}
}

// RemoveVirtualGroup 删除一个虚拟组（路由组 CRUD 用）。
func (r *ModelRouter) RemoveVirtualGroup(name string) {
	r.mu.Lock()
	delete(r.virtual, name)
	delete(r.virtualStrategy, name)
	delete(r.virtualRules, name)
	delete(r.rotateCounters, name)
	r.mu.Unlock()
}

// ClearVirtualGroups 清空全部虚拟组，供配置变更后整体重建。
func (r *ModelRouter) ClearVirtualGroups() {
	r.mu.Lock()
	r.virtual = make(map[string][]string, 8)
	r.virtualStrategy = make(map[string]string, 8)
	r.virtualRules = make(map[string][]GroupRule, 8)
	r.rotateCounters = make(map[string]*atomic.Uint64, 8)
	r.mu.Unlock()
}

// SetVirtualGroupRules 设置虚拟组的组规则（intent / effort / time）。
// rules 为空时清除。规则只重排候选、不改变候选集合，判定见 group_rule.go。
func (r *ModelRouter) SetVirtualGroupRules(name string, rules []GroupRule) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(rules) == 0 {
		delete(r.virtualRules, name)
		return
	}
	cp := make([]GroupRule, len(rules))
	copy(cp, rules)
	r.virtualRules[name] = cp
}

// HasGroup 报告某个名字是否是当前虚拟路由组。
func (r *ModelRouter) HasGroup(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.virtual[name]
	return ok
}

// GroupStrategy 返回虚拟组的策略名。
func (r *ModelRouter) GroupStrategy(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, ok := r.virtualStrategy[name]; ok {
		return s
	}
	return ""
}

// Resolve 返回按优先级排序的候选绑定。
// SetEnabledFilter 注入"渠道是否启用"的判定（渠道页的 on/off）。
func (r *ModelRouter) SetEnabledFilter(fn func(string) bool) {
	if fn == nil {
		r.enabledFilter.Store(nil)
		return
	}
	r.enabledFilter.Store(&fn)
}

// providerEnabled 报告渠道是否允许参与路由（未注入时全部允许）。
func (r *ModelRouter) providerEnabled(pid string) bool {
	if fn := r.enabledFilter.Load(); fn != nil && *fn != nil {
		return (*fn)(pid)
	}
	return true
}

// filterEnabled 去掉停用渠道的绑定。
func (r *ModelRouter) filterEnabled(in []modelBinding) []modelBinding {
	if len(in) == 0 {
		return in
	}
	out := make([]modelBinding, 0, len(in))
	for _, b := range in {
		if r.providerEnabled(b.ProviderID) {
			out = append(out, b)
		}
	}
	return out
}

func (r *ModelRouter) Resolve(model string) []modelBinding {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.resolveLocked(model, "", "", false)
}

// ResolveSmart 在 Resolve 的基础上为 smart 虚拟组启用意图分类。
//
//   - text 为空：没有可分类的内容，等价 Resolve；
//   - strategy 非 smart：applyGroupStrategy 只给 smart 组注入 Intent，
//     其余策略行为与 Resolve 完全一致；
//   - fromRouter：本次请求来自网关自身的意图分类调用。此时绝不能再分类，
//     否则"分类模型调用分类模型"会无限递归（分类模型常配置在同一个
//     虚拟组里，就算不在，两个 smart 组互相调用也是死循环）。防线两层：
//     HTTP 头 X-Aether-Intent 由 serve 识别，这里把 allowIntent 置 false，
//     最终让 RouteContext.Intent 保持 nil。
//   - sessionKey：跨轮稳定的会话指纹，供分类器做"短追问继承"。
func (r *ModelRouter) ResolveSmart(model, text, sessionKey string, fromRouter bool) []modelBinding {
	if strings.TrimSpace(text) == "" {
		return r.Resolve(model)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.resolveLocked(model, text, sessionKey, !fromRouter)
}

// ResolveSmartEffort 是 ResolveSmart 的增强版：多接受请求显式要求的
// reasoning effort（组规则的 effort 条件读它），并返回解析附加输出
// （命中的规则序号与规则要求的 UseEffort）。
//
// 为什么单独留一个入口而不是改 ResolveSmart 的签名：dispatch 之外还有大量
// 只关心候选链的调用方（诊断页、自检、分类器自身），让它们继续用简单签名，
// 只有真正要把规则结果写回请求的路径才需要 outcome。
func (r *ModelRouter) ResolveSmartEffort(model, text, sessionKey, effort string, fromRouter bool) ([]modelBinding, RouteOutcome) {
	return r.ResolveSmartEffortSignals(model, text, sessionKey, effort, fromRouter, RouteSignals{})
}

// ResolveSmartEffortSignals 在 ResolveSmartEffort 之上再带上请求信号
// （agent / folder / tokens / images），供组规则的对应条件使用。
func (r *ModelRouter) ResolveSmartEffortSignals(model, text, sessionKey, effort string, fromRouter bool, sig RouteSignals) ([]modelBinding, RouteOutcome) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if strings.TrimSpace(text) == "" {
		return r.resolveLockedOut(model, "", "", effort, false, sig)
	}
	return r.resolveLockedOut(model, text, sessionKey, effort, !fromRouter, sig)
}

// resolveLocked 是解析核心。text/sessionKey 非空且 allowIntent 时，
// smart 组的 RouteContext 会带上文本、会话指纹与意图分类函数。
// 调用方必须已持有 r.mu.RLock。
func (r *ModelRouter) resolveLocked(model, text, sessionKey string, allowIntent bool) []modelBinding {
	out, _ := r.resolveLockedOut(model, text, sessionKey, "", allowIntent)
	return out
}

// RouteSignals 是组规则的"请求信号"条件（agents / folders / tokens / images）。
//
// 为什么单独抽一个结构：这些条件要由 dispatch 从请求里读出来，但路由层
// 不该反过来解析协议。用零值表示"没有信号"，此时相关条件一律不成立。
type RouteSignals struct {
	Tokens int
	Images bool
	Agent  string
	Folder string
}

// resolveLockedOut 是解析核心的完整版：多接受请求的 reasoning effort，并把
// 策略与组规则的附加输出（RouteOutcome）带回给调用方。
// 调用方必须已持有 r.mu.RLock。
func (r *ModelRouter) resolveLockedOut(model, text, sessionKey, effort string, allowIntent bool, sig ...RouteSignals) ([]modelBinding, RouteOutcome) {
	var none RouteOutcome

	// 1. 精确匹配
	if b, ok := r.exact[model]; ok {
		return r.filterEnabled([]modelBinding{b}), none
	}

	// 2. 模型重定向：用户显式配置的虚拟名优先于同名裸模型与路由组。
	//    即使目标渠道暂时都不可用也直接返回空链，避免同名真实模型被
	//    静默顶替——重定向是用户明确选择的语义。
	if list, ok := r.redirects[normalizeModelKey(model)]; ok {
		return r.orderByReady(list), none
	}

	// 3. 虚拟聚合组。chain 里的 group/<id> 在这里递归展开（展开逻辑是
	//    纯函数，见 group_rule.go 的 expandGroupChain），带环检测与最大深度；
	//    展开结果再依次过策略与组规则。
	if _, ok := r.virtual[model]; ok {
		seats, warns := r.groupSeatsLocked(model, text, sessionKey, allowIntent)
		logGroupWarnings(warns)
		seats = r.filterEnabled(seats)
		if len(seats) > 0 {
			return r.applyGroupStrategyAndRules(model, seats, text, sessionKey, effort, allowIntent, sig...)
		}
	}

	// 4. 带前缀但未登记：显式渠道前缀优先于裸名别名。
	//    例如 cursor/auto 不是聊天渠道、不在 exact 表里，但 "auto" 同时是
	//    qoder/trae/workbuddy 的通用模型名；如果先走别名合并，请求会被
	//    错误地分到别的渠道，连自检都会报"无法路由回自己的渠道"。
	if i := strings.Index(model, "/"); i > 0 {
		pid := model[:i]
		upstream := model[i+1:]
		if _, ok := provider.Get(pid); ok {
			return []modelBinding{{ProviderID: pid, UpstreamModel: upstream}}, none
		}
	}

	// 5. 裸模型名 + 归一化/规范名合并。
	//    精确同名（例如 Qoder 的 "DeepSeek-V4.1-Flash"）不能独占：还要并入
	//    canonical/别名命中的其他渠道，才能跨渠道失败转移。
	var merged []modelBinding
	if list, ok := r.bare[model]; ok {
		for _, b := range list {
			merged = appendBindingUnique(merged, b)
		}
	}
	if list, ok := r.alias[normalizeModelKey(model)]; ok {
		for _, b := range list {
			merged = appendBindingUnique(merged, b)
		}
	}
	if len(merged) > 0 {
		return r.orderByReady(merged), none
	}

	// 6. 模型家族兜底：客户端发了未登记的新版本号（如 claude-sonnet-4-6-20260101），
	//    按家族关键词匹配到提供同族模型的渠道。
	//
	//    刻意不做"任意请求都路由到所有渠道"的兜底：那会让"模型名拼错"
	//    变成"静默成功但用错了模型"，是比报错严重得多的故障。
	if family := modelFamily(model); family != "" {
		out := make([]modelBinding, 0, 2)
		for key, list := range r.bare {
			if modelFamily(key) != family {
				continue
			}
			for _, b := range list {
				// 家族兜底只用于"同代未登记的新版本号"；V4 与 V4.1 这种
				// 跨代模型不能被当成同族静默替换，否则回退时会偷换模型。
				if !bindingMatchesVersion(model, b) {
					continue
				}
				// 家族内优先用上游登记的模型名，而不是客户端传来的未知版本号。
				out = appendBindingUnique(out, b)
			}
		}
		if len(out) > 0 {
			return r.orderByReady(out), none
		}
	}

	// 7. 无法判断：返回空，由网关给出明确错误（含可用模型清单）。
	return nil, none
}

// mergeCatalogModels 合并上游实时目录与本地静态清单：live 优先（元数据更新），
// 静态清单补 live 没提到的项。
//
// 为什么要合并而不是覆盖：上游 /models 经常漏项（实测 workbuddy-cn 的目录里没有
// deepseek-v4.1-flash，但该模型名实际可用）。一旦被 live 整体覆盖，清单里写好的
// 模型就从路由表里消失，客户端发裸名再也匹配不到它。
//
// 去重按 normalizeModelKey 而不是原始 ID：上游常把 DeepSeek-V4-Pro 写成
// deepseek-v4-pro，只按精确 ID 去重会让同一模型在清单里出现两次。
func mergeCatalogModels(live, static []ir.ModelMeta) []ir.ModelMeta {
	seen := make(map[string]bool, len(live))
	out := make([]ir.ModelMeta, 0, len(live)+len(static))
	for _, m := range live {
		out = append(out, m)
		seen[modelMergeKey(m)] = true
	}
	for _, m := range static {
		if seen[modelMergeKey(m)] {
			continue
		}
		out = append(out, m)
	}
	return out
}

// modelMergeKey 是合并去重用的归一化键；ID 为空时退回原始文本，避免误合并。
func modelMergeKey(m ir.ModelMeta) string {
	if k := normalizeModelKey(m.ID); k != "" {
		return k
	}
	return strings.ToLower(strings.TrimSpace(m.ID))
}

// appendBindingUnique 按渠道+上游模型去重后追加绑定。
func appendBindingUnique(list []modelBinding, b modelBinding) []modelBinding {
	for _, x := range list {
		if x.ProviderID == b.ProviderID && x.UpstreamModel == b.UpstreamModel {
			return list
		}
	}
	return append(list, b)
}

// orderByReady 优先有可用账号的渠道，其次按注册顺序（Priority）。
// 只影响顺序，不丢渠道：不可用渠道仍留在链尾，等前序都失败后照常尝试。
func (r *ModelRouter) orderByReady(list []modelBinding) []modelBinding {
	out := r.filterEnabled(append([]modelBinding(nil), list...))
	if len(out) < 2 {
		return out
	}
	lookup := r.readyLookup.Load()
	if lookup == nil || *lookup == nil {
		return out
	}
	ready := *lookup
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := ready(out[i].ProviderID), ready(out[j].ProviderID)
		if ri != rj {
			return ri
		}
		return out[i].Priority < out[j].Priority
	})
	return out
}

// groupRouteContextLocked 构造策略与组规则共用的只读上下文。
// text/sessionKey 是意图路由的输入；allowIntent 为 false 时
// RouteContext.Intent 保持 nil——网关自调用的分类请求走这里防递归。
// 调用方必须已持有 r.mu.RLock（resolveLocked 的路径保证），这里直接读内部
// map；不能在这里再 RLock——同 goroutine 递归 RLock 遇到排队中的写锁会死锁。
func (r *ModelRouter) groupRouteContextLocked(group, text, sessionKey string, allowIntent bool) *RouteContext {
	ctx := &RouteContext{Group: group, Text: text, SessionKey: sessionKey}
	// 计数器在 SetVirtualGroupWithStrategy 里只创建一次、之后不再替换，
	// 捕获指针是安全的；Add 本身是原子的。轮转计数器由框架持有而不是
	// 策略自己记账，策略因此保持纯函数。
	counter := r.rotateCounters[group]
	ctx.Rotate = func(n int) int {
		if n <= 0 || counter == nil {
			return 0
		}
		k := counter.Add(1)
		return int((k - 1) % uint64(n))
	}
	if lookup := r.quotaLookup.Load(); lookup != nil && *lookup != nil {
		ctx.Quota = *lookup
	}
	if lookup := r.latencyLookup.Load(); lookup != nil && *lookup != nil {
		ctx.Latency = *lookup
	}
	// 意图分类：smart 策略要用；带 intent 条件的组规则也要用。两者都只在
	// "下游真实请求 + 有文本"的路径上注入，否则防递归就失效了。
	if allowIntent && (r.virtualStrategy[group] == GroupStrategySmart || rulesNeedIntent(r.virtualRules[group])) {
		if fn := r.intentFn.Load(); fn != nil && *fn != nil {
			ctx.Intent = *fn
		}
	}
	return ctx
}

// applyGroupStrategy 为虚拟组构造 RouteContext 并套用排序策略。
// 调用方必须已持有 r.mu.RLock。
func (r *ModelRouter) applyGroupStrategy(group string, seats []modelBinding, text, sessionKey string, allowIntent bool) []modelBinding {
	ctx := r.groupRouteContextLocked(group, text, sessionKey, allowIntent)
	strategy := r.virtualStrategy[group]
	if strategy == "" {
		strategy = GroupStrategyPriority
	}
	return applyRouteStrategy(strategy, ctx, seats)
}

// groupSeatsLocked 把虚拟组的 chain 展开成候选链（group/<id> 递归展开）。
// 展开本身是纯函数（expandGroupChain），这里只负责把路由表与内层组策略
// 接进去。
//
// 内层组自己声明了 routing 时先按它排序；没声明时策略是 priority（空操作），
// 外层策略随后作用于整条展开链——这就是「内层组默认继承外层 routing」
// （对齐 aether Group.Within）。
// 调用方必须已持有 r.mu.RLock。
func (r *ModelRouter) groupSeatsLocked(group, text, sessionKey string, allowIntent bool) ([]modelBinding, []string) {
	return expandGroupChain(groupChainSource{
		Chains:      r.virtual,
		DefaultSeat: defaultBindingFor,
		Segment: func(gid string, seats []modelBinding) []modelBinding {
			return r.applyGroupStrategy(gid, seats, text, sessionKey, allowIntent)
		},
	}, group)
}

// applyRouteStrategy 是策略执行的统一封装（学 aether 的 routes.Apply）：
//  1. 策略 panic 时 recover 并退回原序——fail-open。路由是请求的必经之路，
//     一个有 bug 的策略绝不能把整个网关打挂；顺序退化最多损失一点均摊效果。
//  2. 策略返回后校验结果是 seats 的一个排列（同长度且 ProviderID 多重集合相等），
//     否则退回原序。防两类 bug：丢渠道（排在后面的上游永远收不到流量）与
//     私自篡改绑定（请求被发到策略自己杜撰的渠道上）。
func applyRouteStrategy(name string, ctx *RouteContext, seats []modelBinding) []modelBinding {
	original := make([]modelBinding, len(seats))
	copy(original, seats)

	fn, ok := routeStrategies[name]
	if !ok || fn == nil {
		log.Printf("[WARN] 路由策略 %q 未注册，按 %s 原序处理", name, GroupStrategyPriority)
		fn = routeStrategies[GroupStrategyPriority]
	}

	result := func() (out []modelBinding) {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("[WARN] 路由策略 %q panic: %v，退回原序", name, p)
				out = original
			}
		}()
		return fn(ctx, seats)
	}()

	if !isPermutation(original, result) {
		log.Printf("[WARN] 路由策略 %q 返回结果不是候选集的排列，退回原序", name)
		return original
	}
	return result
}

// isPermutation 判断 got 是否为 want 的排列：同长度且 ProviderID 多重集合相等。
func isPermutation(want, got []modelBinding) bool {
	if len(want) != len(got) {
		return false
	}
	counts := make(map[string]int, len(want))
	for _, b := range want {
		counts[b.ProviderID]++
	}
	for _, b := range got {
		counts[b.ProviderID]--
		if counts[b.ProviderID] < 0 {
			return false
		}
	}
	return true
}

// strategyPriority 原样返回配置顺序——固定优先级链。纯函数，不做任何重排。
func strategyPriority(_ *RouteContext, seats []modelBinding) []modelBinding {
	return seats
}

// strategyRotate 以 ctx.Rotate 给出的偏移为起点轮转候选链，把负载均摊到各渠道。
func strategyRotate(ctx *RouteContext, seats []modelBinding) []modelBinding {
	// 框架未接计数器时偏移取 0（原序），策略自身绝不崩溃。
	offset := 0
	if ctx != nil && ctx.Rotate != nil {
		offset = ctx.Rotate(len(seats))
	}
	if offset <= 0 {
		return seats
	}
	if offset >= len(seats) {
		// 契约保证 offset ∈ [0, n)，这里兜底防御异常实现。
		offset %= len(seats)
	}
	rotated := make([]modelBinding, 0, len(seats))
	rotated = append(rotated, seats[offset:]...)
	rotated = append(rotated, seats[:offset]...)
	return rotated
}

// paceBandRatio 定义"同一分带"的相对宽度：rate 相差 10% 以内视为同带。
const paceBandRatio = 1.10

// strategyPace 按"距重置每小时可消耗额度"（rate = remaining / max(hours, 0.5)）
// 降序排列，把即将清零的额度优先消耗掉，避免白白过期。
//
// 预计算分带（学 aether 的 pace）：rate 落在同一 10% 等比带内的渠道视为并列，
// 借助稳定排序保持原序。直接拿"差 <10% 视为相等"做比较器不满足传递性
// （A≈B、B≈C 但 A<C），排序结果会依赖比较顺序、不可复现；量化成分带后
// 比较器退化为严格全序，任何实现都得出一致的排列。
func strategyPace(ctx *RouteContext, seats []modelBinding) []modelBinding {
	now := time.Now().Unix()
	// 分带与绑定必须成对排序：若只排绑定而用"原下标"查分带，
	// 排序中的每次交换都会让下标与分带错位、比较器失效。
	type pacedSeat struct {
		binding modelBinding
		band    int
	}
	paced := make([]pacedSeat, len(seats))
	for i, b := range seats {
		band := -1 // 无额度数据的渠道排最后，之间保持原序
		if ctx == nil || ctx.Quota == nil {
			paced[i] = pacedSeat{binding: b, band: band}
			continue
		}
		remaining, resetAt, ok := ctx.Quota(b.ProviderID)
		if ok {
			hours := float64(resetAt-now) / 3600
			// resetAt 为 0 或已过（hours 非正）时 max 自然钳到 0.5：
			// 视为"半小时内可消耗 remaining 原值"。
			rate := float64(remaining) / math.Max(hours, 0.5)
			band = paceBand(rate)
		}
		paced[i] = pacedSeat{binding: b, band: band}
	}

	sort.SliceStable(paced, func(i, j int) bool { return paced[i].band > paced[j].band })

	out := make([]modelBinding, len(seats))
	for i, s := range paced {
		out[i] = s.binding
	}
	return out
}

// strategyUsage 按"剩余额度最多"排序（不考虑到期时间）。
//
// 与 pace 的区别：pace 算的是"剩余 / 距到期小时数"，目的是把临期额度先花掉；
// usage 只看剩余量，目的是"先把额度多的号用掉"（保住小号做兜底）。
// 以前组级的 usage 被直接映射成 pace，用户选了 usage 拿到的是另一套语义。
func strategyUsage(ctx *RouteContext, seats []modelBinding) []modelBinding {
	if ctx == nil || ctx.Quota == nil {
		return seats
	}
	type row struct {
		binding   modelBinding
		remaining int
		known     bool
	}
	rows := make([]row, len(seats))
	for i, b := range seats {
		rows[i].binding = b
		if remaining, _, ok := ctx.Quota(b.ProviderID); ok {
			rows[i].remaining, rows[i].known = remaining, true
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].known != rows[j].known {
			return rows[i].known
		}
		if !rows[i].known {
			return false
		}
		return rows[i].remaining > rows[j].remaining
	})
	out := make([]modelBinding, len(seats))
	for i, r := range rows {
		out[i] = r.binding
	}
	return out
}

// paceBand 把 rate 量化到 10% 等比带宽的分带序号（越大越急）。
// rate <= 0 归入 0 号带（有额度数据但剩余为 0，仍排在无数据渠道之前）。
func paceBand(rate float64) int {
	if rate <= 0 {
		return 0
	}
	return int(math.Floor(math.Log(rate) / math.Log(paceBandRatio)))
}

// strategySmart 意图路由：把分类器认为"最擅长处理这个提问"的渠道移到链首，
// 其余保持相对顺序——fallback 链语义完整保留：命中渠道失败时依旧按原顺序
// 依次尝试后面的渠道。
//
// 框架契约层面的说明：RouteStrategy 要求实现是纯函数，smart 是唯一的例外
// ——ctx.Intent 背后是一次真实的模型调用（有缓存 / 冷却 / inflight 状态）。
// 这与 rotate 依赖框架持有的计数器同理："有状态"的部分由框架注入，策略
// 本身只负责"拿到序号后怎么排"；分类器 panic 会被 applyRouteStrategy 的
// recover 捕获并退回原序，可用性不受影响。
//
// Intent 未注入（未配置 / 防递归）或返回 0 / 负数 / 越界下标时维持原序，
// smart 就地退化为 priority。
func strategySmart(ctx *RouteContext, seats []modelBinding) []modelBinding {
	if ctx == nil || ctx.Intent == nil {
		return seats
	}
	i := ctx.Intent(ctx.Group, ctx.SessionKey, seats, ctx.Text)
	if i <= 0 || i >= len(seats) {
		return seats
	}
	out := make([]modelBinding, 0, len(seats))
	out = append(out, seats[i])
	out = append(out, seats[:i]...)
	out = append(out, seats[i+1:]...)
	return out
}

// SetQuotaLookup 注入渠道额度查询函数（由网关在启动时把账号池接进来）。
// 传 nil 等于撤掉额度数据，pace 策略保持原序。
func (r *ModelRouter) SetQuotaLookup(fn QuotaLookup) {
	if fn == nil {
		r.quotaLookup.Store(nil)
		return
	}
	r.quotaLookup.Store(&fn)
}

// SetLatencyLookup 注入渠道时延查询函数（由网关把账号池接进来）。
// 传 nil 等于关闭 fast 策略的排序依据，就地退化为 priority。
func (r *ModelRouter) SetLatencyLookup(fn LatencyLookup) {
	if fn == nil {
		r.latencyLookup.Store(nil)
		return
	}
	r.latencyLookup.Store(&fn)
}

// strategyFast 按实测时延排序：首字节快的优先，并列比吞吐。
// 没有样本的渠道排在已有样本之后，并保持它们之间的原序（不与无从比较的渠道混排）。
func strategyFast(ctx *RouteContext, seats []modelBinding) []modelBinding {
	type ranked struct {
		binding modelBinding
		ttft    float64
		tps     float64
		known   bool
	}
	rows := make([]ranked, len(seats))
	for i, b := range seats {
		rows[i].binding = b
		if ctx != nil && ctx.Latency != nil {
			if t, p, ok := ctx.Latency(b.ProviderID); ok {
				rows[i].ttft, rows[i].tps, rows[i].known = t, p, true
			}
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.known != b.known {
			return a.known
		}
		if !a.known {
			return false
		}
		if a.ttft != b.ttft {
			return a.ttft < b.ttft
		}
		return a.tps > b.tps
	})
	out := make([]modelBinding, len(rows))
	for i, r := range rows {
		out[i] = r.binding
	}
	return out
}

// SetIntentClassifier 注入意图分类函数（由网关把分类器接进来）。
// 传 nil 等于关闭意图路由，smart 策略就地退化为 priority。
func (r *ModelRouter) SetIntentClassifier(fn IntentClassifyFunc) {
	if fn == nil {
		r.intentFn.Store(nil)
		return
	}
	r.intentFn.Store(&fn)
}

// parseModelGroup 解析虚拟组配置：兼容 ["a","b"] 数组与
// {"chain":["a","b"],"strategy":"rotate"} 对象两种形态。
func parseModelGroup(raw json.RawMessage) ([]string, string, error) {
	var chain []string
	if err := json.Unmarshal(raw, &chain); err == nil {
		if len(chain) == 0 {
			return nil, "", errors.New("渠道链为空")
		}
		return chain, GroupStrategyPriority, nil
	}
	var obj struct {
		Chain    []string `json:"chain"`
		Strategy string   `json:"strategy"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if len(obj.Chain) == 0 {
			return nil, "", errors.New("chain 为空")
		}
		strategy := obj.Strategy
		if strategy == "" {
			strategy = GroupStrategyPriority
		}
		return obj.Chain, strategy, nil
	}
	return nil, "", errors.New("既不是字符串数组也不是 {chain, strategy} 对象")
}

// familyKeywords 是模型家族关键词表。命中即认为"同族可路由"。
var familyKeywords = []string{
	"claude", "gpt", "o1", "o3", "o4", "gemini", "deepseek",
	"qwen", "doubao", "glm", "kimi", "llama", "mistral", "grok",
	"ernie", "hunyuan", "minimax", "spark", "step",
}

// bindingMatchesVersion 报告候选绑定是否与请求的版本代际兼容。
// 请求没有 vX 标签时不限制；请求有标签时，候选必须带同一个标签——
// 完全不标版本的候选（例如 Qoder 的泛槽位 DeepSeek-Flash）也不参与
// 家族兜底，否则 V4 Flash 的请求可能被静默换成 V4.1。
func bindingMatchesVersion(model string, b modelBinding) bool {
	want := modelVersionTag(model)
	if want == "" {
		return true
	}
	for _, name := range []string{b.UpstreamModel, b.DisplayName} {
		if modelVersionTag(name) != want {
			return false
		}
	}
	return true
}

// modelFamily 提取模型名所属家族关键词。
func modelFamily(model string) string {
	lower := strings.ToLower(model)
	for _, kw := range familyKeywords {
		if strings.Contains(lower, kw) {
			return kw
		}
	}
	return ""
}

// defaultBindingFor 取某渠道登记的默认模型（Manifest 顺序第一个）。
func defaultBindingFor(providerID string) (modelBinding, bool) {
	p, ok := provider.Get(providerID)
	if !ok {
		return modelBinding{}, false
	}
	models := p.Manifest().Models
	if len(models) == 0 {
		return modelBinding{}, false
	}
	m := models[0]
	upstream := m.UpstreamModel
	if upstream == "" {
		upstream = m.ID
	}
	// 去掉可能存在的渠道前缀，上游只认裸模型名。
	if i := strings.LastIndex(upstream, "/"); i >= 0 {
		upstream = upstream[i+1:]
	}
	return modelBinding{
		ProviderID:    providerID,
		UpstreamModel: upstream,
		DisplayName:   m.DisplayName,
	}, true
}

// VirtualGroups 返回已定义的虚拟组（供 UI 展示）。
func (r *ModelRouter) VirtualGroups() map[string][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string][]string, len(r.virtual))
	for k, v := range r.virtual {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// VirtualGroupInfo 是虚拟组的完整定义（含策略）。
type VirtualGroupInfo struct {
	Chain    []string `json:"chain"`
	Strategy string   `json:"strategy"`
}

// VirtualGroupDetails 返回虚拟组定义与策略（供 UI 与自检展示）。
func (r *ModelRouter) VirtualGroupDetails() map[string]VirtualGroupInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]VirtualGroupInfo, len(r.virtual))
	for k, v := range r.virtual {
		chain := make([]string, len(v))
		copy(chain, v)
		strategy := r.virtualStrategy[k]
		if strategy == "" {
			strategy = GroupStrategyPriority
		}
		out[k] = VirtualGroupInfo{Chain: chain, Strategy: strategy}
	}
	return out
}

// KnownModels 返回全部已登记的模型（供 /v1/models）。
func (r *ModelRouter) KnownModels() []ir.ModelMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]bool, len(r.exact))
	out := make([]ir.ModelMeta, 0, len(r.exact))

	for _, p := range provider.All() {
		m := p.Manifest()
		models := m.Models
		if src := r.modelSource.Load(); src != nil {
			if effective := (*src)(m.ID); effective != nil {
				// 目录模式（merge / fallback / allowlist）已在模型来源里
				// 处理；路由层不再自行合并，否则 fallback 与 allowlist
				// 会被这里的 merge 抵消。
				models = effective
			}
		}
		for _, mm := range models {
			fullID := mm.ID
			if !strings.Contains(fullID, "/") {
				fullID = m.ID + "/" + mm.ID
			}
			if seen[fullID] {
				continue
			}
			seen[fullID] = true
			meta := mm
			meta.ID = fullID
			out = append(out, meta)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// HasProvider 判断渠道是否在索引里。
func (r *ModelRouter) HasProvider(providerID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, pid := range r.chatProviders {
		if pid == providerID {
			return true
		}
	}
	return false
}

// AvailableModelIDs 返回已登记的模型 ID 列表（用于错误提示）。
func (r *ModelRouter) AvailableModelIDs(limit int) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, 0, len(r.exact))
	for id := range r.exact {
		out = append(out, id)
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// handleResolveModel GET /api/models/resolve?model=... —— 路由诊断：
// 返回该模型名会命中的渠道链（顺序、上游模型、可用账号数）。
func (g *Gateway) handleResolveModel(w http.ResponseWriter, r *http.Request) {
	model := strings.TrimSpace(r.URL.Query().Get("model"))
	if model == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 model 参数"})
		return
	}
	bindings := g.models.Resolve(model)
	rows := make([]map[string]any, 0, len(bindings))
	for _, b := range bindings {
		envs := g.pool.ListByProvider(b.ProviderID)
		available := 0
		for _, e := range envs {
			if e != nil && !e.Disabled() && e.Available() {
				available++
			}
		}
		name := b.ProviderID
		if p, ok := provider.Get(b.ProviderID); ok {
			name = p.Manifest().DisplayName
		}
		rows = append(rows, map[string]any{
			"provider":       b.ProviderID,
			"name":           name,
			"upstream_model": b.UpstreamModel,
			"ready":          available > 0,
			"accounts":       len(envs),
			"available":      available,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": model, "bindings": rows})
}
