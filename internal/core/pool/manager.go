// Package pool 是账号池与路由层。
//
// 它要回答的问题只有一个：这次的请求，应该用哪个渠道的哪个账号？
//
// 三级漏斗（顺序不可颠倒，否则会破坏上下文连续性）：
//  1. 状态过滤：只留可用账号，跳过冷却 / 耗尽 / 失效；
//  2. 粘性命中：同一会话强制回原账号，保证上游 session 上下文与缓存命中；
//  3. 加权负载：新会话按"剩余额度 × 临期权重 / 并发"打分，优先榨干即将过期的积分。
package pool

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
	"strings"
)

// Manager 管理全部渠道的全部账号。
type Manager struct {
	mu sync.RWMutex
	// byProvider 渠道 ID -> 账号列表。
	byProvider map[string][]*provider.AccountEnv
	// index 账号 ID -> 环境，用于按 ID 精确定位。
	index map[string]*provider.AccountEnv
	// sticky 会话指纹 -> 账号绑定（带过期）。
	sticky map[string]stickyBind
	// rotate 渠道 -> 轮转计数器（Routing=rotate 时用）。
	rotate map[string]*atomic.Uint64
	// sunk 账号 ID -> 429 后仍有额度、按 Sink 设置排到队尾。
	sunk map[string]bool
	// dirty 标记账号集合发生变化，需要落盘。
	dirty int32
	// persist 账号持久化。
	persist persistence
}

type stickyBind struct {
	accountID string
	expiresAt time.Time
	boundAt   time.Time
	// cacheTokens 是上一次该会话观测到的 prompt-cache 读写量。
	// 缓存量低于 affinityMinCache 时会话亲和几乎没有收益，TTL 缩短到 5 分钟。
	cacheTokens int
}

// 会话亲和的时长与阈值（对齐 aether 的 affinity 量化思路）：
//   - 缓存价值足（≥1024 tokens）：保 15 分钟，让上游 prompt cache 持续命中；
//   - 缓存价值不足：5 分钟后就不再强留，避免为了没收益的亲和把流量钉死在慢账号上。
const (
	stickyTTLWarm    = 15 * time.Minute
	stickyTTLCold    = 5 * time.Minute
	affinityMinCache = 1024
)

// New 创建账号池。
func New() *Manager {
	return &Manager{
		byProvider: make(map[string][]*provider.AccountEnv, 8),
		index:      make(map[string]*provider.AccountEnv, 32),
		sticky:     make(map[string]stickyBind, 256),
		rotate:     make(map[string]*atomic.Uint64, 8),
		sunk:       make(map[string]bool, 8),
	}
}

// Add 加入一个账号。
func (m *Manager) Add(env *provider.AccountEnv) {
	if env == nil || env.AccountID == "" {
		return
	}
	m.mu.Lock()
	if _, exists := m.index[env.AccountID]; exists {
		// 覆盖语义：重新登录时会替换同一账号的凭证与客户端。
		m.replaceLocked(env)
		m.mu.Unlock()
		atomic.StoreInt32(&m.dirty, 1)
		return
	}
	m.index[env.AccountID] = env
	m.byProvider[env.ProviderID] = append(m.byProvider[env.ProviderID], env)
	m.mu.Unlock()

	atomic.StoreInt32(&m.dirty, 1)
}

func (m *Manager) replaceLocked(env *provider.AccountEnv) {
	old := m.index[env.AccountID]
	oldProvider := old.ProviderID
	list := m.byProvider[oldProvider]
	for i, e := range list {
		if e.AccountID == env.AccountID {
			list[i] = env
			break
		}
	}
	m.index[env.AccountID] = env
	if oldProvider != env.ProviderID {
		// 渠道变更：从旧渠道移除，挂到新渠道。
		m.removeFromListLocked(oldProvider, env.AccountID)
		m.byProvider[env.ProviderID] = append(m.byProvider[env.ProviderID], env)
	}
}

func (m *Manager) removeFromListLocked(providerID, accountID string) {
	list := m.byProvider[providerID]
	out := list[:0]
	for _, e := range list {
		if e.AccountID != accountID {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		delete(m.byProvider, providerID)
		return
	}
	m.byProvider[providerID] = out
}

// Remove 移除一个账号。
func (m *Manager) Remove(accountID string) bool {
	m.mu.Lock()
	env, ok := m.index[accountID]
	if !ok {
		m.mu.Unlock()
		return false
	}
	delete(m.index, accountID)
	m.removeFromListLocked(env.ProviderID, accountID)
	m.mu.Unlock()

	atomic.StoreInt32(&m.dirty, 1)
	return true
}

// Get 按 ID 取账号。
func (m *Manager) Get(accountID string) (*provider.AccountEnv, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.index[accountID]
	return e, ok
}

// ListByProvider 返回某渠道的全部账号。
func (m *Manager) ListByProvider(providerID string) []*provider.AccountEnv {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.byProvider[providerID]
	out := make([]*provider.AccountEnv, len(src))
	copy(out, src)
	return out
}

// All 返回全部账号。
func (m *Manager) All() []*provider.AccountEnv {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*provider.AccountEnv, 0, len(m.index))
	for _, e := range m.index {
		out = append(out, e)
	}
	return out
}

// SelectOptions 路由选项。
type SelectOptions struct {
	// PinnedAccount 来自 previous_response_id 的强绑定，优先级最高，不做负载评估。
	PinnedAccount string
	// SessionKey 用于粘性命中。
	SessionKey string
	// Exclude 已尝试且失败的账号，重试时排除。
	Exclude map[string]bool
	// Model 是本次要请求的上游模型，用于账号级模型白名单过滤。
	Model string
	// Mode 是渠道的账号选择方式（渠道编辑器 Routing）：
	// ""/smart（默认加权负载）、order、rotate、weight、usage、pace、fast、free。
	Mode string
	// Affinity 是会话粘性档位（渠道编辑器 Stays）：
	// ""/auto、session、turn、off、strict。turn 无可靠的轮次信号，按 auto 处理。
	Affinity string
}

// Select 在指定渠道内按三级漏斗选一个账号。
func (m *Manager) Select(providerID string, opts SelectOptions) (*provider.AccountEnv, *ir.APIError) {
	m.mu.RLock()
	accounts := make([]*provider.AccountEnv, len(m.byProvider[providerID]))
	copy(accounts, m.byProvider[providerID])
	stickyAccount, stickyHit := m.sticky[opts.SessionKey]
	m.mu.RUnlock()

	if len(accounts) == 0 {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount, "渠道 "+providerID+" 尚未添加任何账号").WithProvider(providerID)
	}

	// 0 级：强绑定（previous_response_id 指定），只要它还可用就必须用它。
	if opts.PinnedAccount != "" {
		if env, ok := m.Get(opts.PinnedAccount); ok && env.Available() && !opts.Exclude[env.AccountID] {
			return env, nil
		}
		// 绑定的账号挂了：继续往下走，实现"跨账号故障转移"。
		// 此时历史由 StateSuture 回填，上下文不会丢。
	}

	// 1 级：状态过滤。
	now := time.Now()
	cands := make([]*provider.AccountEnv, 0, len(accounts))
	for _, env := range accounts {
		if opts.Exclude[env.AccountID] {
			continue
		}
		if !env.Available() {
			continue
		}
		// 官方通道被暂停（鉴权拒绝/手动）的账号不再参与路由；
		// 恢复走 /api/accounts/{id}/official/resume 或检查授权。
		if paused, _, _ := env.OfficialPaused(); paused {
			continue
		}
		if !env.ServesModel(opts.Model) {
			continue
		}
		cands = append(cands, env)
	}
	if len(cands) == 0 {
		return nil, noAvailableError(providerID, accounts)
	}

	// 1.5 级：Sink —— 429 后仍有额度、按渠道设置"排到队尾"的账号放到最后。
	cands = m.orderBySink(cands)

	// 1.6 级：模型级冷却与窗口 cap —— 只调整顺序，不排除。
	//
	// 为什么不直接排除：
	//   - 模型级冷却是"这个模型的这个号刚被限流"，但如果全池都在冷却，
	//     排除完就没有候选了，等于自己把请求打死；留作最后手段更稳。
	//   - 窗口 cap 同理：全池都到 cap 时仍然要能发（用户可能就想跑完这一轮）。
	// 排序后仍在链里，失败会继续往下试，语义与 aether 的"降级而非拒绝"一致。
	cands = orderByModelRest(cands, opts.Model, now)
	cands = orderByCap(cands, now)

	// 2 级：粘性命中（受渠道 Stays 控制）。
	//   auto/turn：TTL 内命中（turn 没有可靠的轮次信号，按 auto 近似）；
	//   session：整段会话留在同一账号（命中即续期 24h）；
	//   strict：绑定的账号不可用时把错误交给调用方，绝不换号；
	//   off：不认粘性，每次重新选。
	affinity := strings.ToLower(strings.TrimSpace(opts.Affinity))
	if affinity != "off" && stickyHit &&
		(affinity == "session" || affinity == "strict" || now.Before(stickyAccount.expiresAt)) {
		for _, env := range cands {
			if env.AccountID != stickyAccount.accountID {
				continue
			}
			// 粘性不能绕过"这个号刚被限流/已达窗口 cap"：
			// 排序已经把这类账号挪到末尾，粘性如果直接返回，等于把
			// "先别用它"的策略整条废掉（sticky 命中率越高越严重）。
			if cooling, _ := env.ModelCoolingDown(opts.Model); cooling {
				break
			}
			if held, _, _ := env.CapHeld(now); held {
				break
			}
			if affinity == "session" {
				m.mu.Lock()
				if b, ok := m.sticky[opts.SessionKey]; ok {
					b.expiresAt = now.Add(24 * time.Hour)
					m.sticky[opts.SessionKey] = b
				}
				m.mu.Unlock()
			}
			m.clearSunk(env.AccountID)
			return env, nil
		}
		if affinity == "strict" {
			return nil, ir.NewAPIError(ir.ErrCodeNoAccount,
				"Stays=Strict：会话绑定的账号当前不可用（限流/额度 cap），按设置不切换到其他账号").WithProvider(providerID)
		}
	}

	// 2.5 级：用户设了首选（priority>0）时，先按显式顺序取最靠前的。
	if bestPri := minPriority(cands); bestPri > 0 {
		tier := make([]*provider.AccountEnv, 0, len(cands))
		for _, env := range cands {
			if env.Priority() == bestPri {
				tier = append(tier, env)
			}
		}
		cands = tier
	}

	// 2.7 级：官方订阅优先（渠道声明 OfficialFirst 且有区分能力时）。
	// 显式 priority 已在上一级决定顺序；这里只把官方账号提进候选，
	// 中转账号保留为故障转移（官方账号被 Exclude/冷却后自然落回）。
	if providerPrefersOfficial(providerID) {
		official := make([]*provider.AccountEnv, 0, len(cands))
		for _, env := range cands {
			if accountIsOfficial(env) {
				official = append(official, env)
			}
		}
		if len(official) > 0 && len(official) < len(cands) {
			cands = official
		}
	}

	// 3 级：渠道的 Routing。smart（默认）落到下面的加权负载；其余模式各自成序。
	if pick := m.pickByMode(providerID, opts.Mode, cands, now); pick != nil {
		m.clearSunk(pick.AccountID)
		return pick, nil
	}

	// 3.5 级：用户权重。设了不同权重时按权重比例随机分流；
	// 全默认（1）时沿用下面的确定性得分，行为与此前一致。
	if pick := weightedPick(cands, now); pick != nil {
		m.clearSunk(pick.AccountID)
		return pick, nil
	}

	// 3.6 级：加权负载。
	var best *provider.AccountEnv
	bestScore := -1.0
	for _, env := range cands {
		s := weightedScore(env, now)
		if s > bestScore {
			bestScore = s
			best = env
		}
	}
	if best == nil {
		// 理论上不可达，兜底取第一个。
		best = cands[0]
	}
	m.clearSunk(best.AccountID)
	return best, nil
}

// providerPrefersOfficial 读取渠道 Manifest 的 OfficialFirst 声明。
func providerPrefersOfficial(providerID string) bool {
	p, ok := provider.Get(providerID)
	if !ok {
		return false
	}
	return p.Manifest().Capabilities.OfficialFirst
}

// accountIsOfficial 由渠道判断凭证是否官方订阅登录态。
func accountIsOfficial(env *provider.AccountEnv) bool {
	p, ok := provider.Get(env.ProviderID)
	if !ok {
		return false
	}
	cap, ok := p.(provider.OfficialAccountCapability)
	return ok && cap.IsOfficialAccount(env.Credentials)
}

// pickByMode 按渠道选择方式挑一个账号；smart/空/free 返回 nil（走加权负载）。
func (m *Manager) pickByMode(providerID, mode string, cands []*provider.AccountEnv, now time.Time) *provider.AccountEnv {
	if len(cands) == 0 {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "order", "manual":
		return cands[0]
	case "rotate":
		m.mu.Lock()
		c := m.rotate[providerID]
		if c == nil {
			c = &atomic.Uint64{}
			m.rotate[providerID] = c
		}
		m.mu.Unlock()
		k := c.Add(1) - 1
		return cands[int(k%uint64(len(cands)))]
	case "weight":
		return weightedPickByWeight(cands)
	case "usage":
		// 剩余额度最多的优先（积分制渠道的"用得最少"）。
		return maxBy(cands, func(e *provider.AccountEnv) float64 {
			if q := e.Quota(); q != nil && q.Remaining > 0 {
				return float64(q.Remaining)
			}
			return 0
		})
	case "pace":
		// 每小时剩余额度最多的优先（临期额度先花）。
		return maxBy(cands, func(e *provider.AccountEnv) float64 {
			q := e.Quota()
			if q == nil || q.Remaining <= 0 {
				return 0
			}
			hours := float64(q.ExpiresAt-now.Unix()) / 3600
			if hours <= 0 {
				hours = 1
			}
			return float64(q.Remaining) / hours
		})
	case "fast":
		// 首字最快（TTFT 升序），没有样本的排最后；并列看吞吐。
		var best *provider.AccountEnv
		var bestTTFT, bestTPS float64
		for _, e := range cands {
			ttft, tps, ok := e.Latency()
			if !ok {
				continue
			}
			if best == nil || ttft < bestTTFT || (ttft == bestTTFT && tps > bestTPS) {
				best, bestTTFT, bestTPS = e, ttft, tps
			}
		}
		if best != nil {
			return best
		}
		return cands[0]
	}
	return nil
}

// weightedPickByWeight 只按用户权重随机分流（Routing=weight）。
func weightedPickByWeight(cands []*provider.AccountEnv) *provider.AccountEnv {
	if len(cands) == 1 {
		return cands[0]
	}
	weights := make([]float64, len(cands))
	total := 0.0
	for i, e := range cands {
		w := float64(e.Weight())
		if w <= 0 {
			w = 1
		}
		weights[i] = w
		total += w
	}
	if total <= 0 {
		return cands[0]
	}
	r := rand.Float64() * total
	for i, e := range cands {
		if r < weights[i] {
			return e
		}
		r -= weights[i]
	}
	return cands[len(cands)-1]
}

// maxBy 返回打分最高的候选（并列取靠前的）。
func maxBy(cands []*provider.AccountEnv, score func(*provider.AccountEnv) float64) *provider.AccountEnv {
	var best *provider.AccountEnv
	bestScore := -1.0
	for _, e := range cands {
		s := score(e)
		if s > bestScore {
			bestScore, best = s, e
		}
	}
	if best == nil {
		return cands[0]
	}
	return best
}

// orderByModelRest 把"该模型正在冷却"的账号排到末尾。
//
// 只排序不排除：全池都在冷却时仍要留一次尝试机会（上游有时限流是瞬时的），
// 但正常情况下正常账号应该先被选中。
func orderByModelRest(cands []*provider.AccountEnv, model string, now time.Time) []*provider.AccountEnv {
	if model == "" || len(cands) < 2 {
		return cands
	}
	head := make([]*provider.AccountEnv, 0, len(cands))
	tail := make([]*provider.AccountEnv, 0, len(cands))
	for _, env := range cands {
		if cooling, _ := env.ModelCoolingDown(model); cooling {
			tail = append(tail, env)
		} else {
			head = append(head, env)
		}
	}
	if len(tail) == 0 {
		return cands
	}
	return append(head, tail...)
}

// orderByCap 把"已触到窗口 cap"的账号排到末尾。
//
// cap 是用户设的"用到 X% 就少用它"，属排序偏好而非不可用：
// 全池都到 cap 时仍然要能发请求，否则等于自己把自己锁死。
func orderByCap(cands []*provider.AccountEnv, now time.Time) []*provider.AccountEnv {
	if len(cands) < 2 {
		return cands
	}
	head := make([]*provider.AccountEnv, 0, len(cands))
	tail := make([]*provider.AccountEnv, 0, len(cands))
	for _, env := range cands {
		if held, _, _ := env.CapHeld(now); held {
			tail = append(tail, env)
		} else {
			head = append(head, env)
		}
	}
	if len(tail) == 0 {
		return cands
	}
	return append(head, tail...)
}

// orderBySink 把"429 后仍有额度"的账号排到候选末尾。
func (m *Manager) orderBySink(cands []*provider.AccountEnv) []*provider.AccountEnv {
	m.mu.RLock()
	sunk := make(map[string]bool, len(m.sunk))
	for k, v := range m.sunk {
		sunk[k] = v
	}
	m.mu.RUnlock()
	if len(sunk) == 0 {
		return cands
	}
	head := make([]*provider.AccountEnv, 0, len(cands))
	tail := make([]*provider.AccountEnv, 0, len(cands))
	for _, e := range cands {
		if sunk[e.AccountID] {
			tail = append(tail, e)
		} else {
			head = append(head, e)
		}
	}
	if len(tail) == 0 {
		return cands
	}
	return append(head, tail...)
}

// SetSink 标记/清除某账号的"沉底"状态（渠道设了 Rate limited=Goes to the back 时用）。
func (m *Manager) SetSink(accountID string, on bool) {
	if accountID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sunk == nil {
		m.sunk = make(map[string]bool, 8)
	}
	if on {
		m.sunk[accountID] = true
	} else {
		delete(m.sunk, accountID)
	}
}

// clearSunk 在账号被选中后解除沉底，下一轮重新排队。
func (m *Manager) clearSunk(accountID string) {
	m.mu.Lock()
	if m.sunk != nil {
		delete(m.sunk, accountID)
	}
	m.mu.Unlock()
}

// minPriority 返回候选里最小的正优先级；没有则 0。
func minPriority(envs []*provider.AccountEnv) int {
	best := 0
	for _, e := range envs {
		p := e.Priority()
		if p > 0 && (best == 0 || p < best) {
			best = p
		}
	}
	return best
}

// weightedPick 在用户设了不同权重时按 (权重 × 负载得分) 随机挑一个；
// 全部权重为 1 或只有一个候选时返回 nil（走确定性得分）。
func weightedPick(cands []*provider.AccountEnv, now time.Time) *provider.AccountEnv {
	if len(cands) < 2 {
		return nil
	}
	differ := false
	weights := make([]float64, len(cands))
	total := 0.0
	for i, e := range cands {
		w := e.Weight()
		if w != 1 {
			differ = true
		}
		s := weightedScore(e, now)
		if s <= 0 {
			s = 0.01
		}
		weights[i] = float64(w) * s
		total += weights[i]
	}
	if !differ || total <= 0 {
		return nil
	}
	r := rand.Float64() * total
	for i, e := range cands {
		if r < weights[i] {
			return e
		}
		r -= weights[i]
	}
	return cands[len(cands)-1]
}

// weightedScore 计算账号得分。分数越高越优先。
func weightedScore(env *provider.AccountEnv, now time.Time) float64 {
	remaining := 1.0
	urgency := 1.0

	if q := env.Quota(); q != nil {
		remaining = float64(q.Remaining)
		if remaining <= 0 {
			// 额度未知或为 0 时给一个最小权重，仍留机会（有些渠道不暴露额度）。
			remaining = 0.1
		}
		if q.ExpiresAt > 0 {
			hours := float64(q.ExpiresAt-now.Unix()) / 3600
			// 24 小时内清零的额度，权重从 1 线性抬升到 2：优先消耗临期积分。
			if hours > 0 && hours < 24 {
				urgency = 1 + (24-hours)/24
			}
		}
	}

	return remaining * urgency / float64(env.Inflight()+1)
}

// BindSticky 记录会话与账号的绑定关系。
func (m *Manager) BindSticky(sessionKey, accountID string) {
	if sessionKey == "" || accountID == "" {
		return
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sticky[sessionKey] = stickyBind{
		accountID: accountID,
		boundAt:   now,
		expiresAt: now.Add(stickyTTLWarm),
	}
	// 顺带清理过期绑定，避免无界增长。
	if len(m.sticky) > 4096 {
		for k, v := range m.sticky {
			if now.After(v.expiresAt) {
				delete(m.sticky, k)
			}
		}
	}
}

// NoteStickyCache 记录某会话上一次的 prompt-cache 读写量，并按缓存价值调整亲和时长。
//
// 这是"亲和值不值得保"的量化依据：缓存量大 → 换账号会丢掉整段前缀缓存，
// 值得多留一会儿；缓存量小 → 换个更快的账号反而更划算。
func (m *Manager) NoteStickyCache(sessionKey string, cachedTokens int) {
	if sessionKey == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.sticky[sessionKey]
	if !ok {
		return
	}
	b.cacheTokens = cachedTokens
	if cachedTokens < affinityMinCache {
		if cold := b.boundAt.Add(stickyTTLCold); cold.Before(b.expiresAt) {
			b.expiresAt = cold
		}
	}
	m.sticky[sessionKey] = b
}

// StickyAccount 查询会话已绑定的账号。
func (m *Manager) StickyAccount(sessionKey string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	b, ok := m.sticky[sessionKey]
	if !ok || time.Now().After(b.expiresAt) {
		return "", false
	}
	return b.accountID, true
}

// SessionFingerprint 计算一个稳定的会话指纹（无 state store 时的降级方案）。
func SessionFingerprint(providerID string, req *ir.UnifiedChatRequest) string {
	h := sha256.New()
	h.Write([]byte(providerID))
	h.Write([]byte{0})
	// 第一级：客户端自报的会话标识（OpenAI 的 user / Anthropic 的
	// metadata.user_id）。最准，也不受历史被裁剪的影响。
	if v, ok := req.Meta["client_session"].(string); ok && strings.TrimSpace(v) != "" {
		h.Write([]byte("sid:"))
		h.Write([]byte(strings.TrimSpace(v)))
		return hex.EncodeToString(h.Sum(nil))[:32]
	}
	// 第三级：来源信息（IP + UA + 凭证），客户端既没自报会话 ID、
	// 又没有可用文本时用它兜底（relay-panel 的同一个梯子）。
	if v, ok := req.Meta["client_fp"].(string); ok && strings.TrimSpace(v) != "" {
		h.Write([]byte("fp:"))
		h.Write([]byte(strings.TrimSpace(v)))
		return hex.EncodeToString(h.Sum(nil))[:32]
	}
	// 第二级：首条用户文本 + 最后一条用户文本，兼顾"同一会话"与"不同提问"。
	first, last := "", ""
	for _, it := range req.Items {
		if it.IsUserTurn() && it.Text != "" {
			if first == "" {
				first = it.Text
			}
			last = it.Text
		}
	}
	h.Write([]byte(first))
	h.Write([]byte{0})
	h.Write([]byte(last))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// EarliestReset 返回该渠道最近一次恢复的等待时长（0 = 没有已知恢复时间）。
// 全池冷却时用它给调用方一个真实的 Retry-After。
func (m *Manager) EarliestReset(providerID string) time.Duration {
	m.mu.RLock()
	list := append([]*provider.AccountEnv(nil), m.byProvider[providerID]...)
	m.mu.RUnlock()
	now := time.Now()
	var best time.Duration
	for _, e := range list {
		state, _, resetAt := e.StateDetail()
		if state != provider.StateCoolingDown && state != provider.StateDepleted {
			continue
		}
		d := resetAt.Sub(now)
		if d <= 0 {
			d = time.Second
		}
		if best == 0 || d < best {
			best = d
		}
	}
	return best
}

// noAvailableError 说清"为什么一个号都选不出来"。
//
// 客户端（Qoder / DSH / Cline 等）多数把 message 原样显示给用户，只写
// "无可用账号"看不出是额度用尽、登录失效还是正在冷却，也不知道何时恢复，
// 只能笼统显示"系统发生异常"。这里把原因拆开并给出最早恢复时间。
func noAvailableError(providerID string, accounts []*provider.AccountEnv) *ir.APIError {
	var depleted, invalid, cooling, disabled int
	var soonest time.Time
	for _, env := range accounts {
		state, _, resetAt := env.StateDetail()
		switch {
		case env.Disabled():
			disabled++
		case state == provider.StateDepleted:
			depleted++
		case state == provider.StateInvalid:
			invalid++
		case state == provider.StateCoolingDown, state == provider.StateRefreshing:
			cooling++
		default:
			// 状态可用却被过滤，只剩"不服务该模型"或官方暂停两种可能。
			disabled++
		}
		if !resetAt.IsZero() && (soonest.IsZero() || resetAt.Before(soonest)) {
			soonest = resetAt
		}
	}

	parts := make([]string, 0, 4)
	if depleted > 0 {
		parts = append(parts, fmt.Sprintf("%d 个额度用尽", depleted))
	}
	if invalid > 0 {
		parts = append(parts, fmt.Sprintf("%d 个登录失效", invalid))
	}
	if cooling > 0 {
		parts = append(parts, fmt.Sprintf("%d 个冷却中", cooling))
	}
	if disabled > 0 {
		parts = append(parts, fmt.Sprintf("%d 个不服务该模型或已停用", disabled))
	}

	msg := "渠道 " + providerID + " 当前无可用账号"
	if len(parts) > 0 {
		msg += "（" + strings.Join(parts, "、") + "）"
	}
	if !soonest.IsZero() {
		msg += "，最早 " + soonest.Local().Format("01-02 15:04") + " 恢复"
	}
	return ir.NewAPIError(ir.ErrCodeNoAccount, msg).WithProvider(providerID)
}
