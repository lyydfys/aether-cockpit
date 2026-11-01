package provider

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// AccountState 账号五态。
//
// 实际路由中真正需要区分行为的只有三态（Ready / CoolingDown / Invalid），
// Depleted 与 Refreshing 是为了给 UI 提供准确文案并防止并发刷新。
type AccountState string

const (
	// StateReady 可用。
	StateReady AccountState = "ready"
	// StateCoolingDown 触发 429 后的冷却窗口。
	StateCoolingDown AccountState = "cooling_down"
	// StateDepleted 当日免费额度用尽，等 ResetAt 恢复。
	StateDepleted AccountState = "depleted"
	// StateInvalid 凭证失效，需要重新登录。
	StateInvalid AccountState = "invalid"
	// StateRefreshing 正在刷新凭证，短暂锁定避免并发重复刷新。
	StateRefreshing AccountState = "refreshing"
)

// DeviceFingerprint 持久化设备指纹。
//
// 一号一沙箱的核心：同一个账号在它的整个生命周期内必须始终上报完全相同的指纹组合，
// 否则上游风控会判定为"账号被盗用/多设备共享"。因此这个结构必须落盘且不可再生。
type DeviceFingerprint struct {
	MachineID     string `json:"machine_id"`
	DeviceID      string `json:"device_id"`
	MAC           string `json:"mac,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
	Platform      string `json:"platform,omitempty"`
	// Extra 承载渠道特有的指纹字段（如 install_id、session_id 常量）。
	Extra map[string]string `json:"extra,omitempty"`
}

// Clone 深拷贝，避免调用方误改共享指纹。
func (f DeviceFingerprint) Clone() DeviceFingerprint {
	cp := f
	if len(f.Extra) > 0 {
		cp.Extra = make(map[string]string, len(f.Extra))
		for k, v := range f.Extra {
			cp.Extra[k] = v
		}
	}
	return cp
}

// AccountEnv 是一个账号的完整运行时环境。
//
// 铁律：绝不允许多个账号共用一个全局 http.Client。
// 独立 Client = 独立 CookieJar + 独立 TLS 会话缓存 + 独立代理配置 + 独立连接池，
// 这是"账号之间绝不在网络层发生任何串味"的物理保证。
type AccountEnv struct {
	AccountID  string `json:"account_id"`
	ProviderID string `json:"provider_id"`
	Label      string `json:"label,omitempty"`

	// Credentials AccessToken / RefreshToken / Cookie 等，落盘时加密。
	Credentials map[string]string `json:"credentials"`
	Fingerprint DeviceFingerprint `json:"fingerprint"`

	// HTTPClient 该账号独占的客户端（已绑定指纹与网络出口）。
	HTTPClient *http.Client `json:"-"`
	// NetworkPolicy 该账号生效的网络策略，来自 ProviderManifest。
	NetworkPolicy NetworkPolicy `json:"network_policy"`
	// TLSProfile 该账号使用的 TLS 指纹，如 "chrome_131"。
	// 空表示用标准库默认 ClientHello（特征明显，容易被识别为非官方客户端）。
	TLSProfile string `json:"tls_profile,omitempty"`
	// ProxyOverride 是该账号显式代理："" 跟随全局，"direct" 强制直连，其余是代理 URL。
	ProxyOverride string `json:"proxy_override,omitempty"`

	mu       sync.RWMutex
	state    AccountState
	stateAt  time.Time
	stateMsg string
	resetAt  time.Time
	// inflight 当前活跃并发，用于加权路由的负载惩罚项，也用于并发闸。
	inflight int
	// rpmTokens / rpmAt 是每账号令牌桶（每分钟请求上限）。
	rpmTokens float64
	rpmAt     time.Time
	// 时延观测：首字节与吞吐的指数滑动平均，供 fast 路由策略使用。
	ttftEWMA float64 // 毫秒
	tpsEWMA  float64 // token/秒
	ttftN    int
	// consecutiveFailures 连续失败计数，用于自动熔断。
	consecutiveFailures int
	lastUsed            time.Time
	// quota 缓存最近一次配额查询结果，供加权路由使用。
	quota *QuotaStatus
	// rateWindows 是 429 时观测到的窗口（渠道没有用量接口时的真实兜底）。
	rateWindows map[string]rateWindowState
	// tags 用户标签（批量管理 / 筛选用，随账号落盘）。
	tags []string
	// note 用户备注（与标签分开：标签用于分组筛选，备注是自由文本）。
	note string
	// createdAt 账号首次入库时间（落盘保留，不随后续保存变化）。
	createdAt time.Time
	// disabled 是用户在渠道编辑页手动停用的账号（不参与路由）。
	disabled bool
	// priority >0 表示用户把它设为首选（数字越小越先）；0 表示默认。
	priority int
	// weight 是用户在渠道编辑页给账号设的权重（1..1000）：
	// 同优先级/同状态下，按权重比例分流，而不是固定给同一个账号。
	weight int
	// concurrency 是该账号的并发上限覆盖（nil=跟随全局；0=不限制）。
	concurrency *int
	// allowedModels 非空时，该账号只服务这些模型。
	allowedModels []string
	// modelMapping 是账号级"客户端名 -> 上游名"映射（sub2api 的 model_mapping）。
	// 键可为精确名或含 * 的通配（如 claude-*）；精确优先，其次最长模式。
	modelMapping map[string]string
	// officialPaused 官方通道暂停：鉴权被拒或用户手动暂停后置位，
	// 账号不再参与路由，直到检查授权通过或手动恢复。
	officialPaused      bool
	officialPauseReason string
	officialPausedAt    time.Time
}

// NewAccountEnv 创建一个处于 Ready 状态的账号环境。
func NewAccountEnv(accountID, providerID string) *AccountEnv {
	return &AccountEnv{
		AccountID:   accountID,
		ProviderID:  providerID,
		state:       StateReady,
		stateAt:     time.Now(),
		createdAt:   time.Now(),
		Credentials: make(map[string]string),
	}
}

// State 返回当前状态。
func (e *AccountEnv) State() AccountState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

// StateDetail 返回状态与附带说明（给 UI 展示"为什么不可用"）。
func (e *AccountEnv) StateDetail() (AccountState, string, time.Time) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state, e.stateMsg, e.resetAt
}

// Available 判断账号此刻能否承接请求。
func (e *AccountEnv) Available() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.availableLocked(time.Now())
}

func (e *AccountEnv) availableLocked(now time.Time) bool {
	if e.disabled {
		return false
	}
	switch e.state {
	case StateReady:
		return true
	case StateCoolingDown, StateDepleted:
		// 时间窗口到期自动恢复，避免需要外部定时器驱动状态迁移。
		if !e.resetAt.IsZero() && now.After(e.resetAt) {
			e.state = StateReady
			e.stateMsg = ""
			e.resetAt = time.Time{}
			e.stateAt = now
			return true
		}
		return false
	default:
		return false
	}
}

// MarkCoolingDown 记录一次限流冷却。
func (e *AccountEnv) MarkCoolingDown(d time.Duration, reason string) {
	if d <= 0 {
		d = 60 * time.Second
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = StateCoolingDown
	e.stateAt = time.Now()
	e.resetAt = time.Now().Add(d)
	e.stateMsg = reason
	e.consecutiveFailures++
}

// MarkDepleted 标记额度耗尽。
func (e *AccountEnv) MarkDepleted(resetAt time.Time, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = StateDepleted
	e.stateAt = time.Now()
	e.resetAt = resetAt
	e.stateMsg = reason
}

// MarkRefreshing 标记正在刷新凭证。
//
// 这个状态是"短暂锁定"：刷新期间该账号不参与路由，避免并发请求用旧 token
// 去上游撞 401，也避免多个刷新循环重复刷新同一账号。
func (e *AccountEnv) MarkRefreshing() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = StateRefreshing
	e.stateAt = time.Now()
	e.stateMsg = "正在刷新凭证"
}

// MarkInvalid 标记凭证失效，需要重新登录。
func (e *AccountEnv) MarkInvalid(reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = StateInvalid
	e.stateAt = time.Now()
	e.stateMsg = reason
}

// RestoreState 把账号恢复到刷新前的状态。
//
// 用于"刷新失败但原状态仍然有效"的场景：临时网络错误不应把冷却/耗尽
// 状态误清成 Ready，也不应把原本可用的账号直接判死。
func (e *AccountEnv) RestoreState(state AccountState, msg string, resetAt time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = state
	e.stateMsg = msg
	e.resetAt = resetAt
	e.stateAt = time.Now()
}

// MarkReady 手动恢复为可用（用户重新登录或手动解除熔断后调用）。
func (e *AccountEnv) MarkReady() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = StateReady
	e.stateMsg = ""
	e.resetAt = time.Time{}
	e.stateAt = time.Now()
	e.consecutiveFailures = 0
}

// MarkSuccess 一次成功调用后清零失败计数。
func (e *AccountEnv) MarkSuccess() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.consecutiveFailures = 0
	e.lastUsed = time.Now()
}

// MarkFailure 记录一次失败，连续失败超过阈值自动熔断。
func (e *AccountEnv) MarkFailure(threshold int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.consecutiveFailures++
	e.lastUsed = time.Now()
	if threshold > 0 && e.consecutiveFailures >= threshold && e.state == StateReady {
		e.state = StateCoolingDown
		e.stateAt = time.Now()
		e.resetAt = time.Now().Add(5 * time.Minute)
		e.stateMsg = "连续失败达到阈值，自动熔断 5 分钟"
	}
}

// ConsecutiveFailures 返回连续失败次数。
func (e *AccountEnv) ConsecutiveFailures() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.consecutiveFailures
}

// LastUsed 返回最近一次使用时间。
func (e *AccountEnv) LastUsed() time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastUsed
}

// Acquire 占用一个并发位，返回释放函数。
// TryAcquire 尝试占用一个并发槽位。
//
// max<=0 表示不限并发；超过上限返回 ok=false，由调用方按
// "该账号暂时没有容量"处理（换号，或最终给下游 429），而不是当成上游故障惩罚账号。
// 返回的 release 必须在该请求的整条流结束后才调用——首字节就释放等于没有闸门。
func (e *AccountEnv) TryAcquire(max int) (release func(), ok bool) {
	e.mu.Lock()
	if max > 0 && e.inflight >= max {
		e.mu.Unlock()
		return nil, false
	}
	e.inflight++
	e.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			e.inflight--
			e.mu.Unlock()
		})
	}, true
}

// Acquire 是无上限版本的 TryAcquire，保留给只关心计数的调用方。
func (e *AccountEnv) Acquire() func() {
	release, _ := e.TryAcquire(0)
	return release
}

// AllowRequest 按每分钟上限做令牌桶限流（rpm<=0 表示不限）。
// 桶容量等于 rpm，即允许一分钟的量突发打满；之后按 rpm/60 每秒回填。
func (e *AccountEnv) AllowRequest(rpm int, now time.Time) bool {
	if rpm <= 0 {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rpmAt.IsZero() {
		e.rpmAt = now
		e.rpmTokens = float64(rpm)
	}
	rate := float64(rpm) / 60.0
	elapsed := now.Sub(e.rpmAt).Seconds()
	if elapsed > 0 {
		e.rpmTokens += elapsed * rate
		if e.rpmTokens > float64(rpm) {
			e.rpmTokens = float64(rpm)
		}
		e.rpmAt = now
	}
	if e.rpmTokens < 1 {
		return false
	}
	e.rpmTokens--
	return true
}

// Inflight 返回当前活跃并发数。
func (e *AccountEnv) Inflight() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.inflight
}

// NoteLatency 记录一次请求的时延观测（α=0.3 指数滑动平均）。
// 只统计拿到首字节且产出过 token 的请求：失败请求的 0 值会把估计带偏。
func (e *AccountEnv) NoteLatency(ttftMS, outputTokens, durationMS int64) {
	if ttftMS <= 0 || outputTokens <= 0 || durationMS <= ttftMS {
		return
	}
	tps := float64(outputTokens) / (float64(durationMS-ttftMS) / 1000)
	if tps <= 0 {
		return
	}
	const alpha = 0.3
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ttftN == 0 {
		e.ttftEWMA = float64(ttftMS)
		e.tpsEWMA = tps
	} else {
		e.ttftEWMA = alpha*float64(ttftMS) + (1-alpha)*e.ttftEWMA
		e.tpsEWMA = alpha*tps + (1-alpha)*e.tpsEWMA
	}
	if e.ttftN < 1<<30 {
		e.ttftN++
	}
}

// Latency 返回当前时延估计；还没有样本时 ok=false。
func (e *AccountEnv) Latency() (ttftMS, tps float64, ok bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.ttftN == 0 {
		return 0, 0, false
	}
	return e.ttftEWMA, e.tpsEWMA, true
}

// SetQuota 缓存配额快照。
func (e *AccountEnv) SetQuota(q *QuotaStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.quota = q
}

// Quota 返回缓存的配额快照（可能为 nil）。
func (e *AccountEnv) Quota() *QuotaStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.quota
}

// rateWindowState 是一次 429 观测到的窗口状态。
type rateWindowState struct {
	used     float64
	resetsAt time.Time
	readAt   time.Time
}

// NoteRateLimit 记录一次限流窗口：上游 429 说明这个窗口此刻已经打满。
//
// 窗口名按冷却时长推断（≤6h 视为 5 小时窗口，更长视为每周窗口）；
// 这只是观测值，渠道自己实现了用量接口时以那个读数为准。
func (e *AccountEnv) NoteRateLimit(d time.Duration, now time.Time) {
	if d <= 0 {
		return
	}
	name := "Quota"
	switch {
	case d <= 6*time.Hour:
		name = "5 hours"
	case d > 6*time.Hour:
		name = "Weekly"
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.rateWindows == nil {
		e.rateWindows = make(map[string]rateWindowState, 2)
	}
	e.rateWindows[name] = rateWindowState{used: 100, resetsAt: now.Add(d), readAt: now}
}

// RateWindows 返回仍在生效的限流窗口观测值（按重置时间升序）。
func (e *AccountEnv) RateWindows() []QuotaWindow {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.rateWindows) == 0 {
		return nil
	}
	now := time.Now()
	out := make([]QuotaWindow, 0, len(e.rateWindows))
	for name, w := range e.rateWindows {
		if !w.resetsAt.After(now) {
			continue
		}
		out = append(out, QuotaWindow{
			Name:     name,
			Used:     w.used,
			ResetsAt: w.resetsAt.Unix(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ResetsAt < out[j].ResetsAt })
	return out
}

// SetDisabled 手动停用/启用账号（不改变其登录状态）。
func (e *AccountEnv) SetDisabled(on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.disabled = on
}

// SetOfficialPause 暂停官方通道（鉴权被拒 / 手动暂停）。
func (e *AccountEnv) SetOfficialPause(reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.officialPaused = true
	e.officialPauseReason = reason
	e.officialPausedAt = time.Now()
}

// SetOfficialPausedAt 用持久化的暂停时间替换当前时间（仅恢复落盘状态时使用）。
func (e *AccountEnv) SetOfficialPausedAt(at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.officialPausedAt = at
}

// ResumeOfficial 恢复官方通道。
func (e *AccountEnv) ResumeOfficial() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.officialPaused = false
	e.officialPauseReason = ""
	e.officialPausedAt = time.Time{}
}

// OfficialPaused 返回官方通道是否暂停，以及原因与时间。
func (e *AccountEnv) OfficialPaused() (bool, string, time.Time) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.officialPaused, e.officialPauseReason, e.officialPausedAt
}

// SetPriority 设置账号的显式优先顺序（1 = 首选，0 = 默认）。
func (e *AccountEnv) SetPriority(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.priority = n
}

// Priority 返回显式优先顺序。
func (e *AccountEnv) Priority() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.priority
}

// SetWeight 设置账号权重（1..1000；越界收敛到边界）。
func (e *AccountEnv) SetWeight(n int) {
	if n < 1 {
		n = 1
	}
	if n > 1000 {
		n = 1000
	}
	e.mu.Lock()
	e.weight = n
	e.mu.Unlock()
}

// Weight 返回账号权重（未设置按 1）。
func (e *AccountEnv) Weight() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.weight < 1 {
		return 1
	}
	return e.weight
}

// SetConcurrencyOverride 设置账号级并发上限（nil=跟随全局；0=不限制）。
func (e *AccountEnv) SetConcurrencyOverride(limit *int) {
	e.mu.Lock()
	if limit == nil {
		e.concurrency = nil
	} else {
		v := *limit
		if v < 0 {
			v = 0
		}
		if v > 1000 {
			v = 1000
		}
		e.concurrency = &v
	}
	e.mu.Unlock()
}

// ConcurrencyOverride 返回账号级并发上限；第二个值表示是否设过。
func (e *AccountEnv) ConcurrencyOverride() (int, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.concurrency == nil {
		return 0, false
	}
	return *e.concurrency, true
}

// SetAllowedModels 设置该账号可服务的模型（空 = 全部）。
func (e *AccountEnv) SetAllowedModels(models []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.allowedModels = append([]string(nil), models...)
}

// AllowedModels 返回模型白名单副本。
func (e *AccountEnv) AllowedModels() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]string(nil), e.allowedModels...)
}

// SetModelMapping 设置账号级模型映射（空 = 不映射）。
func (e *AccountEnv) SetModelMapping(m map[string]string) {
	clean := make(map[string]string, len(m))
	for k, v := range m {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" || k == v {
			continue
		}
		clean[k] = v
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(clean) == 0 {
		e.modelMapping = nil
		return
	}
	e.modelMapping = clean
}

// ModelMapping 返回账号级模型映射副本。
func (e *AccountEnv) ModelMapping() map[string]string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.modelMapping) == 0 {
		return nil
	}
	out := make(map[string]string, len(e.modelMapping))
	for k, v := range e.modelMapping {
		out[k] = v
	}
	return out
}

// MapModel 把上游模型名按账号映射改写；无匹配返回空串。
func (e *AccountEnv) MapModel(model string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.modelMapping) == 0 || model == "" {
		return ""
	}
	if v, ok := e.modelMapping[model]; ok {
		return v
	}
	best := ""
	bestLen := -1
	for pat, v := range e.modelMapping {
		if !strings.Contains(pat, "*") || len(pat) <= bestLen {
			continue
		}
		if matchModelGlob(pat, model) {
			best, bestLen = v, len(pat)
		}
	}
	return best
}

// matchModelGlob 是模型名的单星号通配匹配（支持多个 *）。
func matchModelGlob(pat, name string) bool {
	if pat == "" {
		return false
	}
	parts := strings.Split(pat, "*")
	if len(parts) == 1 {
		return pat == name
	}
	if !strings.HasPrefix(name, parts[0]) {
		return false
	}
	name = name[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(name, mid)
		if i < 0 {
			return false
		}
		name = name[i+len(mid):]
	}
	if last == "" {
		return true
	}
	return strings.HasSuffix(name, last) && len(name) >= len(last)
}

// ServesModel 报告该账号是否服务某模型（白名单为空即全部）。
func (e *AccountEnv) ServesModel(model string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.allowedModels) == 0 || model == "" {
		return true
	}
	prefix := e.ProviderID + "/"
	for _, m := range e.allowedModels {
		// 白名单既可存全名（ch/模型），也可存上游名（模型）；选号拿到的
		// 是上游名，因此两边都认。
		if m == model || strings.TrimPrefix(m, prefix) == model {
			return true
		}
	}
	return false
}

// Disabled 报告账号是否被手动停用。
func (e *AccountEnv) Disabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.disabled
}

// SetTags 覆盖用户标签。
func (e *AccountEnv) SetTags(tags []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cp := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			cp = append(cp, t)
		}
	}
	e.tags = cp
}

// TagsList 返回标签副本。
func (e *AccountEnv) TagsList() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]string, len(e.tags))
	copy(out, e.tags)
	return out
}

// SetNote 设置用户备注。
func (e *AccountEnv) SetNote(note string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.note = strings.TrimSpace(note)
}

// Note 返回用户备注。
func (e *AccountEnv) Note() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.note
}

// SetCreatedAt 恢复落盘的创建时间（零值表示没有记录）。
func (e *AccountEnv) SetCreatedAt(t time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !t.IsZero() {
		e.createdAt = t
	}
}

// CreatedAtUnix 返回创建时间的 Unix 秒；没有记录时返回 0。
func (e *AccountEnv) CreatedAtUnix() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.createdAt.IsZero() {
		return 0
	}
	return e.createdAt.Unix()
}

// Client 返回账号独占的 HTTP 客户端（未设置时退回 http.DefaultClient）。
// 运行中切换代理会用 SetHTTPClient 替换，读取必须走这里以避免数据竞争。
func (e *AccountEnv) Client() *http.Client {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.HTTPClient != nil {
		return e.HTTPClient
	}
	return http.DefaultClient
}

// Proxy 返回账号的显式代理覆盖。
func (e *AccountEnv) Proxy() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.ProxyOverride
}

// SetProxyOverride 设置账号的显式代理覆盖。
func (e *AccountEnv) SetProxyOverride(p string) {
	e.mu.Lock()
	e.ProxyOverride = p
	e.mu.Unlock()
}

// SetHTTPClient 替换账号的 HTTP 客户端（代理热切换用）。
func (e *AccountEnv) SetHTTPClient(c *http.Client) {
	e.mu.Lock()
	e.HTTPClient = c
	e.mu.Unlock()
}

// GetCredential 读取一个凭证字段。
func (e *AccountEnv) GetCredential(key string) string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.Credentials[key]
}

// SetCredential 写入一个凭证字段。
func (e *AccountEnv) SetCredential(key, value string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Credentials == nil {
		e.Credentials = make(map[string]string, 8)
	}
	e.Credentials[key] = value
}

// AccountSnapshot 是用于落盘/UI 展示的只读快照。
type AccountSnapshot struct {
	AccountID        string            `json:"account_id"`
	ProviderID       string            `json:"provider_id"`
	Label            string            `json:"label,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	Note             string            `json:"note,omitempty"`
	CreatedAt        int64             `json:"created_at,omitempty"`
	State            AccountState      `json:"state"`
	StateMsg         string            `json:"state_msg,omitempty"`
	ResetAt          int64             `json:"reset_at,omitempty"`
	Inflight         int               `json:"inflight"`
	Failures         int               `json:"failures"`
	LastUsed         int64             `json:"last_used,omitempty"`
	Quota            *QuotaStatus      `json:"quota,omitempty"`
	FingerprintShort string            `json:"fingerprint_short,omitempty"`
	TLSProfile       string            `json:"tls_profile,omitempty"`
	Priority         int               `json:"priority,omitempty"`
	Weight           int               `json:"weight,omitempty"`
	Disabled         bool              `json:"disabled,omitempty"`
	Concurrency      *int              `json:"concurrency,omitempty"`
	AllowedModels    []string          `json:"allowed_models,omitempty"`
	ModelMapping     map[string]string `json:"model_mapping,omitempty"`
}

// CredentialsSnapshot 返回凭证的深拷贝，供导出/迁移使用。
func (e *AccountEnv) CredentialsSnapshot() map[string]string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.Credentials) == 0 {
		return nil
	}
	out := make(map[string]string, len(e.Credentials))
	for k, v := range e.Credentials {
		out[k] = v
	}
	return out
}

// Snapshot 生成只读快照。
func (e *AccountEnv) Snapshot() AccountSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s := AccountSnapshot{
		AccountID:     e.AccountID,
		ProviderID:    e.ProviderID,
		Label:         e.Label,
		Tags:          e.tags,
		Note:          e.note,
		CreatedAt:     snapCreatedAt(e.createdAt),
		State:         e.state,
		StateMsg:      e.stateMsg,
		Inflight:      e.inflight,
		Failures:      e.consecutiveFailures,
		Quota:         e.quota,
		TLSProfile:    e.TLSProfile,
		Priority:      e.priority,
		Weight:        max(e.weight, 1),
		Disabled:      e.disabled,
		AllowedModels: append([]string(nil), e.allowedModels...),
	}
	if len(e.modelMapping) > 0 {
		s.ModelMapping = make(map[string]string, len(e.modelMapping))
		for k, v := range e.modelMapping {
			s.ModelMapping[k] = v
		}
	}
	if e.concurrency != nil {
		v := *e.concurrency
		s.Concurrency = &v
	}
	if !e.resetAt.IsZero() {
		s.ResetAt = e.resetAt.Unix()
	}
	if !e.lastUsed.IsZero() {
		s.LastUsed = e.lastUsed.Unix()
	}
	if id := e.Fingerprint.DeviceID; len(id) > 8 {
		s.FingerprintShort = id[:8]
	} else {
		s.FingerprintShort = id
	}
	return s
}

// snapCreatedAt 把零值时间写成 0，避免 1970 的时间戳显示成日期。
func snapCreatedAt(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}
