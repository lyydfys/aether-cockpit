package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/eventbus"
	"aether-cockpit/internal/provider"
)

// 订阅额度卡片 —— 对齐 magpie 用量页的 card 契约。
//
// 数据来源按可信度排序：
//  1. 渠道实现的 QuotaCapability（Claude 的 5h/7d OAuth 用量接口）；
//  2. 渠道自己的配额快照（QuotaStatus）；
//  3. 429 时观测到的窗口（NoteRateLimit）——没有用量接口的渠道至少不空白。

const (
	// quotaFreshFor 是卡片读数的新鲜期：超过它，GET 会顺手在后台再读一次。
	quotaFreshFor = 5 * time.Minute
	// quotaReadTimeout 是单账号读取上限（读取只用于展示，不能拖住页面）。
	quotaReadTimeout = 12 * time.Second
)

// quotaCardWindow 是一个窗口的展示形态（百分比 + 重置时间）。
type quotaCardWindow struct {
	Name     string            `json:"name"`
	Used     float64           `json:"used"`
	Amount   int64             `json:"amount,omitempty"`
	Limit    int64             `json:"limit,omitempty"`
	Unit     string            `json:"unit,omitempty"`
	Aside    bool              `json:"aside,omitempty"`
	ResetsAt string            `json:"resetsAt,omitempty"`
	StartsAt string            `json:"startsAt,omitempty"`
	Pool     string            `json:"pool,omitempty"`
	Family   string            `json:"family,omitempty"`
	Members  []quotaCardWindow `json:"members,omitempty"`
}

type quotaCardResets struct {
	Count int `json:"count"`
}

// quotaCardBalancePart 是余额卡片上的一行（标签 + 金额）。
type quotaCardBalancePart struct {
	Label   string  `json:"label,omitempty"`
	Text    string  `json:"text"`
	Percent float64 `json:"percent,omitempty"`
}

// quotaCard 是前端 subscriptions 区的一张卡。
type quotaCard struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Icon     string `json:"icon"`
	User     string `json:"user,omitempty"`
	Alias    string `json:"alias,omitempty"`
	Plan     string `json:"plan,omitempty"`
	Until    int64  `json:"until,omitempty"`
	Balance  string `json:"balance,omitempty"`
	// BalanceParts 是自定义余额接口的多段读数（magpie 的 balanceParts）。
	BalanceParts []quotaCardBalancePart `json:"balanceParts,omitempty"`
	// BalanceTrend 是余额随时间的读数（magpie 的 balanceTrend），前端画走势线。
	BalanceTrend map[string]any `json:"balanceTrend,omitempty"`
	// Resets 是订阅的重置次数（前端显示 ↺ N resets）。
	Resets *quotaCardResets `json:"resets,omitempty"`

	Windows []quotaCardWindow `json:"windows"`

	Error string `json:"error,omitempty"`
	// AsOf / ReadAt 都是毫秒时间戳：前端用 new Date(...) 解析。
	AsOf         int64 `json:"asOf,omitempty"`
	ReadAt       int64 `json:"readAt,omitempty"`
	LastServedAt int64 `json:"lastServedAt,omitempty"`

	// 每日签到：只有实现了签到能力的渠道才会置 Checkins。
	Checkins   bool          `json:"checkins,omitempty"`
	CheckinBy  string        `json:"checkinBy,omitempty"`
	CheckinOff bool          `json:"checkinOff,omitempty"`
	Checkin    *quotaCheckin `json:"checkin,omitempty"`
}

// UsageQuotas 是并发刷新去重表（同一账号同时只读一次）。
type quotaRefreshState struct {
	mu   sync.Mutex
	busy map[string]bool
	// errs 记录最近一次读取失败的原因：没有旧读数的账号会以错误卡出现，
	// 而不是悄悄消失（用户需要知道"为什么这张订阅没有额度读数"）。
	errs map[string]quotaReadFailure
}

type quotaReadFailure struct {
	msg string
	at  time.Time
}

// quotaErrorTTL 是失败原因的展示时长；过期后恢复"后台重试、不占版面"。
const quotaErrorTTL = 30 * time.Minute

func (g *Gateway) quotaState() *quotaRefreshState {
	g.quotaOnce.Do(func() {
		g.quota = &quotaRefreshState{
			busy: make(map[string]bool, 8),
			errs: make(map[string]quotaReadFailure, 8),
		}
	})
	return g.quota
}

func (g *Gateway) noteQuotaFailure(accountID string, err error) {
	if accountID == "" || err == nil {
		return
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	st := g.quotaState()
	st.mu.Lock()
	st.errs[accountID] = quotaReadFailure{msg: msg, at: time.Now()}
	st.mu.Unlock()
}

func (g *Gateway) clearQuotaFailure(accountID string) {
	st := g.quotaState()
	st.mu.Lock()
	delete(st.errs, accountID)
	st.mu.Unlock()
}

// quotaFailure 返回仍在展示期内的失败原因。
func (g *Gateway) quotaFailure(accountID string) (string, time.Time) {
	st := g.quotaState()
	st.mu.Lock()
	defer st.mu.Unlock()
	f, ok := st.errs[accountID]
	if !ok || time.Since(f.at) > quotaErrorTTL {
		return "", time.Time{}
	}
	return f.msg, f.at
}

// usageQuotaCards 把账号池映射成前端卡片。locked=false 时允许后台补读。
func (g *Gateway) usageQuotaCards() ([]quotaCard, bool) {
	if g.pool == nil {
		return nil, false
	}
	envs := g.pool.All()
	cards := make([]quotaCard, 0, len(envs))
	reading := false
	now := time.Now()

	for _, env := range envs {
		p, ok := provider.Get(env.ProviderID)
		if !ok {
			continue
		}
		m := p.Manifest()
		snap := env.Snapshot()
		q := snap.Quota

		windows := make([]quotaCardWindow, 0, 4)
		if q != nil {
			windows = append(windows, mapCardWindows(q.Windows)...)
		}
		// 429 观测到的窗口只补空缺：渠道自己的读数永远优先。
		if len(windows) == 0 {
			for _, w := range env.RateWindows() {
				windows = append(windows, quotaCardWindow{
					Name: w.Name, Used: w.Used, ResetsAt: unixISO(w.ResetsAt),
				})
			}
		}

		// 只有总量/剩余、没有窗口的渠道（积分制）：折成一个额度窗口，
		// 让进度条有东西可画，而不是把数字塞进余额行。
		if len(windows) == 0 && q != nil && q.Total > 0 {
			used := q.Used
			if used <= 0 {
				used = q.Total - q.Remaining
			}
			if used < 0 {
				used = 0
			}
			pct := float64(used) / float64(q.Total) * 100
			if pct > 100 {
				pct = 100
			}
			windows = append(windows, quotaCardWindow{
				Name: "Quota", Used: pct,
				Amount: int64(used), Limit: int64(q.Total), Unit: q.Unit,
				ResetsAt: unixISO(q.ExpiresAt),
			})
		}

		var balanceParts []quotaCardBalancePart
		if q != nil {
			for _, p := range q.BalanceParts {
				balanceParts = append(balanceParts, quotaCardBalancePart{Label: p.Label, Text: p.Text})
			}
		}

		var balanceTrend map[string]any
		if g.quotaHist != nil {
			balanceTrend = g.quotaHist.balanceTrend(env.ProviderID, env.Label)
		}

		balance := ""
		if q != nil && q.Balance > 0 {
			unit := q.BalanceUnit
			if unit == "" {
				unit = "USD"
			}
			balance = formatBalance(q.Balance, unit)
		}

		if len(windows) == 0 && balance == "" && !checkinSupported(p) && snap.State != provider.StateCoolingDown && snap.State != provider.StateInvalid {
			// 没有任何读数：读失败时给一张带原因的卡，否则不拿空卡片占版面。
			if g.providerQuotaReadable(env.ProviderID) && env.Available() {
				if msg, at := g.quotaFailure(env.AccountID); msg != "" {
					card := quotaCard{
						Provider: env.ProviderID,
						Name:     m.DisplayName,
						Icon:     env.ProviderID,
						User:     env.Label,
						Plan:     quotaPlan(q),
						Windows:  []quotaCardWindow{},
						Error:    msg,
						AsOf:     at.UnixMilli(),
					}
					if snap.LastUsed > 0 {
						card.LastServedAt = snap.LastUsed * 1000
					}
					g.annotateCheckin(&card, env, p)
					cards = append(cards, card)
					continue
				}
				if g.ensureQuotaRead(env) {
					reading = true
				}
			}
			continue
		}

		var resets *quotaCardResets
		if q != nil && q.Resets != nil && q.Resets.Count > 0 {
			resets = &quotaCardResets{Count: q.Resets.Count}
		}

		card := quotaCard{
			Provider:     env.ProviderID,
			Name:         m.DisplayName,
			Icon:         env.ProviderID,
			User:         env.Label,
			Plan:         quotaPlan(q),
			Windows:      windows,
			Balance:      balance,
			BalanceParts: balanceParts,
			BalanceTrend: balanceTrend,
			Resets:       resets,
		}
		if q != nil {
			card.Until = q.Until
			// asOf 只表示"这是失败后留下的旧读数"（前端会显示 stale 提示）；
			// 成功读取的时间走 readAt，前端显示 "Updated N ago"。
			card.ReadAt = q.ReadAt * 1000
		}
		if snap.LastUsed > 0 {
			card.LastServedAt = snap.LastUsed * 1000
		}
		if snap.State == provider.StateCoolingDown {
			card.Error = ""
		}
		if snap.State == provider.StateInvalid {
			card.Error = snap.StateMsg
			if card.Error == "" {
				card.Error = "凭证已失效，请重新登录"
			}
		}
		// 读数过期或缺失：后台补读，先给旧值（前端会再拉一次）。
		if g.providerQuotaReadable(env.ProviderID) && env.Available() {
			stale := q == nil || q.ReadAt == 0 || now.Sub(time.Unix(q.ReadAt, 0)) > quotaFreshFor
			if stale && g.ensureQuotaRead(env) {
				reading = true
			}
		}
		g.annotateCheckin(&card, env, p)
		cards = append(cards, card)
	}

	// 远程实例的卡片：读缓存，过期时后台刷新（页面先看到旧读数）。
	remoteCards, remoteReading := g.remoteQuotaCards(false)
	cards = append(cards, remoteCards...)
	if remoteReading {
		reading = true
	}

	sort.SliceStable(cards, func(i, j int) bool {
		if cards[i].Provider != cards[j].Provider {
			return cards[i].Provider < cards[j].Provider
		}
		return cards[i].User < cards[j].User
	})
	return cards, reading
}

// providerQuotaReadable 报告该渠道是否有"能读的东西"：内置用量接口，
// 或用户配置的自定义余额接口。
func (g *Gateway) providerQuotaReadable(pid string) bool {
	p, ok := provider.Get(pid)
	if !ok {
		return false
	}
	if _, ok := provider.AsQuota(p); ok {
		return true
	}
	_, ok = g.providerBalanceConfig(pid)
	return ok
}

// keyQuotaView 是渠道编辑器里"账号行"用的紧凑额度视图。
//
// 与 quotaCard 不同，它只服务一行账号：窗口 + 计划 + 读数时间 + 错误/冷却标记。
// 前端（custom-quota.js）把它画成行内小徽标，不占整页。
type keyQuotaView struct {
	Plan    string            `json:"plan,omitempty"`
	Windows []quotaCardWindow `json:"windows,omitempty"`
	// Used/Total/Unit 用于"只有总量、没有窗口"的积分制渠道。
	Used   int    `json:"used,omitempty"`
	Total  int    `json:"total,omitempty"`
	Unit   string `json:"unit,omitempty"`
	ReadAt int64  `json:"readAt,omitempty"`
	Until  int64  `json:"until,omitempty"`
	Error  string `json:"error,omitempty"`
	// Cooling 表示账号正被网关跳过（429 冷却/额度耗尽），行上显示"冷却中"。
	Cooling bool `json:"cooling,omitempty"`
}

// keyQuotaOf 把账号的额度快照折成 keyQuotaView；没有可展示内容时返回 nil
// （不在账号行上放一个空徽标）。
func keyQuotaOf(e *provider.AccountEnv) *keyQuotaView {
	if e == nil {
		return nil
	}
	snap := e.Snapshot()
	out := &keyQuotaView{}
	if q := snap.Quota; q != nil {
		out.Plan = q.Plan
		out.Windows = mapCardWindows(q.Windows)
		out.Used = q.Used
		out.Total = q.Total
		out.Unit = q.Unit
		out.ReadAt = q.ReadAt * 1000
		out.Until = q.Until
	}
	if snap.State == provider.StateCoolingDown {
		out.Cooling = true
	}
	if snap.State == provider.StateInvalid {
		out.Error = snap.StateMsg
		if out.Error == "" {
			out.Error = "凭证已失效，请重新登录"
		}
	}
	if !out.Cooling && out.Error == "" && len(out.Windows) == 0 && out.Total <= 0 {
		return nil
	}
	return out
}

func quotaPlan(q *provider.QuotaStatus) string {
	if q == nil {
		return ""
	}
	return q.Plan
}

func unixISO(sec int64) string {
	if sec <= 0 {
		return ""
	}
	return time.Unix(sec, 0).Format(time.RFC3339)
}

func formatBalance(v float64, unit string) string {
	if unit == "USD" {
		return "$" + trimFloat(v, 2)
	}
	return trimFloat(v, 2) + " " + unit
}

// trimFloat 保留最多 n 位小数，并去掉尾随零。
func trimFloat(v float64, n int) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		if n < len(s)-dot-1 {
			s = s[:dot+1+n]
		}
		// 只有小数部分才去尾零：整数 40 不能被裁成 4。
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// ensureQuotaRead 在后台读一次该账号的额度；已有读取在进行时返回 true
// （前端据此显示"正在读取"并重试），新发起的读取也返回 true。
func (g *Gateway) ensureQuotaRead(env *provider.AccountEnv) bool {
	st := g.quotaState()
	st.mu.Lock()
	if st.busy[env.AccountID] {
		st.mu.Unlock()
		return true
	}
	st.busy[env.AccountID] = true
	st.mu.Unlock()

	go func() {
		defer func() {
			st.mu.Lock()
			delete(st.busy, env.AccountID)
			st.mu.Unlock()
		}()
		g.readQuotaFor(env, 0)
	}()
	return true
}

// readQuotaFor 同步读一个账号的额度并写回 env；失败不改变账号状态。
func (g *Gateway) readQuotaFor(env *provider.AccountEnv, timeout time.Duration) error {
	p, ok := provider.Get(env.ProviderID)
	if !ok {
		return nil
	}
	cap, hasQuota := provider.AsQuota(p)
	balanceCfg, hasBalance := g.providerBalanceConfigFor(env.ProviderID, env.AccountID)
	if !hasQuota && !hasBalance {
		return nil
	}
	if timeout <= 0 {
		timeout = quotaReadTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var q *provider.QuotaStatus
	var err error
	if hasBalance {
		// 用户显式配了余额接口：它比渠道内置口径更贴近该账号的真实余额。
		q, err = g.queryConfiguredBalance(ctx, env, balanceCfg)
	} else {
		q, err = cap.QueryQuota(ctx, env)
	}
	if err != nil {
		g.noteQuotaFailure(env.AccountID, err)
		return err
	}
	if q == nil {
		return nil
	}
	g.clearQuotaFailure(env.AccountID)
	if q.ReadAt == 0 {
		q.ReadAt = time.Now().Unix()
	}
	env.SetQuota(q)
	if g.quotaHist != nil {
		g.quotaHist.record(env.ProviderID, env.Label, q.Windows, q)
	}
	// 额度耗尽且知道恢复时间时进入 Depleted，避免继续白撞上游。
	if q.Total > 0 && q.Remaining <= 0 && q.ExpiresAt > time.Now().Unix() {
		env.MarkDepleted(time.Unix(q.ExpiresAt, 0), "额度已用尽，等待恢复")
		eventbus.Emit(eventbus.TopicAccount+".state_changed", env.Snapshot())
	}
	return nil
}

// handleUsageQuotas GET /api/usage/quotas?asked=1
//
// 立即返回当前读数；需要补读时用 X-Aether-Cockpit-Reading: 1 告诉前端
// 「有更快的读数在路上，稍后重拉」，而不是让页面等一轮网络往返。
func (g *Gateway) handleUsageQuotas(w http.ResponseWriter, r *http.Request) {
	cards, reading := g.usageQuotaCards()
	if r.URL.Query().Get("asked") == "1" && g.pool != nil {
		// 主动读取：所有支持用量接口的账号都排一次（仍走后台，避免卡住 UI）。
		for _, env := range g.pool.All() {
			if g.providerQuotaReadable(env.ProviderID) && env.Available() {
				g.ensureQuotaRead(env)
				reading = true
			}
		}
	}
	if r.URL.Query().Get("asked") == "1" {
		if _, busy := g.remoteQuotaCards(true); busy {
			reading = true
		}
	}
	if reading {
		w.Header().Set("X-Aether-Reading", "1")
	}
	writeJSON(w, http.StatusOK, cards)
}

// handleUsageQuotasRefresh POST /api/usage/quotas/refresh
//
// 单卡刷新走同步：用户点了刷新就该看到新数字。范围限制在一个渠道，
// 串行读它的账号（订阅用量接口都很轻，不需要抖动）。
func (g *Gateway) handleUsageQuotasRefresh(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(r.URL.Query().Get("provider"))
	user := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("user")))
	if g.pool == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	for _, env := range g.pool.All() {
		if providerID != "" && env.ProviderID != providerID {
			continue
		}
		if user != "" && strings.ToLower(env.Label) != user {
			continue
		}
		if !env.Available() {
			continue
		}
		_ = g.readQuotaFor(env, quotaReadTimeout)
	}

	cards, _ := g.usageQuotaCards()
	writeJSON(w, http.StatusOK, cards)
}

// handleUsageQuotasHistory GET /api/usage/quotas/history
//
// handleUsageQuotasHistory GET /api/usage/quotas/history?days=45
//
// 每个账号每个窗口的读数点（at/left/start/resetsAt），供用量页画曲线。
func (g *Gateway) handleUsageQuotasHistory(w http.ResponseWriter, r *http.Request) {
	days := 45
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 90 {
			days = n
		}
	}
	if g.quotaHist == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, g.quotaHist.view(days))
}

// handleQuotaHistoryHolds GET /api/usage/quotas/history/holds
//
// 估算某个已完成周期里"整个窗口有多大"：期间经过本网关的 token/成本
// 除以厂商给出的已用百分比。参数：provider/user/from/to（ISO）。
func (g *Gateway) handleQuotaHistoryHolds(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	provider := strings.TrimSpace(q.Get("provider"))
	user := strings.TrimSpace(q.Get("user"))
	from, err1 := time.Parse(time.RFC3339, q.Get("from"))
	to, err2 := time.Parse(time.RFC3339, q.Get("to"))
	if provider == "" || user == "" || err1 != nil || err2 != nil || !to.After(from) {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, g.quotaHistoryHolds(provider, user, from, to))
}

// handleUsageArrange POST /api/usage/arrange
//
// 卡片/面板排序属于前端本地偏好（localStorage），服务端不需要持久化；
// 返回成功让前端的乐观更新落定。
func (g *Gateway) handleUsageArrange(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "order": body["order"]})
}

// handleCodexReset POST /api/usage/codex-reset
//
// ChatGPT 订阅的重置额度只能在 Codex 官方客户端中使用，网关不代为消耗
// （不可撤销的操作不做代理）。返回结构化的不可用原因。
func (g *Gateway) handleCodexReset(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"code":    "unavailable",
		"message": "Codex 重置额度只能在 Codex 官方客户端中使用；网关不代为消耗不可撤销的操作",
	})
}

// mapCardWindows 递归映射模型窗口（用于池的成员列表）。
func mapCardWindows(ws []provider.QuotaWindow) []quotaCardWindow {
	out := make([]quotaCardWindow, 0, len(ws))
	for _, w := range ws {
		out = append(out, quotaCardWindow{
			Name: w.Name, Used: w.Used, Amount: w.Amount, Limit: w.Limit, Unit: w.Unit,
			Aside: w.Aside, ResetsAt: unixISO(w.ResetsAt), StartsAt: unixISO(w.StartsAt),
			Pool: w.Pool, Family: w.Family, Members: mapCardWindows(w.Members),
		})
	}
	return out
}
