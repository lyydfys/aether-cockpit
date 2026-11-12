package gateway

// 唤醒任务：按间隔对指定账号发一个最小真实请求，提前触发上游额度重置周期
// （对齐 local-vault 的「唤醒任务 / 一键唤醒」）。
//
// 与签到调度的区别：签到是渠道级 cron + DAG（internal/core/scheduler），
// 唤醒是账号级、间隔驱动、单请求；它绕开池路由直接打选定账号，
// 用最小 max_tokens，成功后立即取消，不进入网关的并发/RPM 闸门。

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const (
	wakeMinInterval   = 15 * time.Minute
	wakeMaxInterval   = 7 * 24 * time.Hour
	wakeDefaultEvery  = 300 // 分钟
	wakeDefaultTokens = 8
	wakeRunTimeout    = 60 * time.Second
	wakeConcurrency   = 2
	wakeTick          = 30 * time.Second
)

// wakeTask 是一条持久化的唤醒任务；每个账号至多一条（按 account_id 去重）。
type wakeTask struct {
	ID           string `json:"id"`
	AccountID    string `json:"account_id"`
	ProviderID   string `json:"provider_id"`
	Label        string `json:"label,omitempty"`
	Model        string `json:"model,omitempty"`
	EveryMinutes int    `json:"every_minutes"`
	MaxTokens    int    `json:"max_tokens,omitempty"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    int64  `json:"created_at"`
	LastRun      int64  `json:"last_run,omitempty"`
	LastModel    string `json:"last_model,omitempty"`
	LastResult   string `json:"last_result,omitempty"`
	LastOK       bool   `json:"last_ok"`
	Failures     int    `json:"failures,omitempty"`
}

type wakeTaskView struct {
	wakeTask
	NextRun int64 `json:"next_run"`
	Running bool  `json:"running"`
}

// initWake 载入持久化任务并启动调度循环。
func (g *Gateway) initWake() {
	g.wakePath = filepath.Join(filepath.Dir(g.settingsPath), "wake.json")
	g.wakeRunning = map[string]bool{}
	g.wakeSem = make(chan struct{}, wakeConcurrency)
	g.loadWake()
	g.goBackground(g.wakeLoop)
}

func (g *Gateway) loadWake() {
	b, err := os.ReadFile(g.wakePath)
	if err != nil {
		return
	}
	var file struct {
		Tasks []*wakeTask `json:"tasks"`
	}
	if json.Unmarshal(b, &file) != nil {
		return
	}
	g.wakeMu.Lock()
	g.wakeTasks = file.Tasks
	g.wakeMu.Unlock()
}

// saveWakeLocked 原子落盘；调用方持有 wakeMu。
func (g *Gateway) saveWakeLocked() {
	if g.wakePath == "" {
		return
	}
	if dir := filepath.Dir(g.wakePath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return
		}
	}
	b, err := json.MarshalIndent(map[string]any{"tasks": g.wakeTasks}, "", "  ")
	if err != nil {
		return
	}
	tmp := g.wakePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, g.wakePath)
}

func (t *wakeTask) interval() time.Duration {
	m := t.EveryMinutes
	if m <= 0 {
		m = wakeDefaultEvery
	}
	return time.Duration(m) * time.Minute
}

// nextRunAt：首轮在创建后的一个间隔，之后按最后执行时间顺延。
func (t *wakeTask) nextRunAt() time.Time {
	base := t.LastRun
	if base == 0 {
		base = t.CreatedAt
	}
	return time.Unix(base, 0).Add(t.interval())
}

func (g *Gateway) wakeLoop() {
	t := time.NewTicker(wakeTick)
	defer t.Stop()
	for {
		var now time.Time
		select {
		case <-g.ctx.Done():
			return
		case now = <-t.C:
		}
		for _, task := range g.dueWakeTasks(now) {
			go g.runWakeTask(task.ID, "schedule")
		}
	}
}

func (g *Gateway) dueWakeTasks(now time.Time) []*wakeTask {
	g.wakeMu.Lock()
	defer g.wakeMu.Unlock()
	var out []*wakeTask
	for _, t := range g.wakeTasks {
		if t == nil || !t.Enabled || g.wakeRunning[t.ID] {
			continue
		}
		if !now.Before(t.nextRunAt()) {
			out = append(out, t)
		}
	}
	return out
}

// runWakeTask 执行一条任务（调度或手动触发）。
func (g *Gateway) runWakeTask(id, reason string) {
	g.wakeMu.Lock()
	var task *wakeTask
	for _, t := range g.wakeTasks {
		if t != nil && t.ID == id {
			task = t
			break
		}
	}
	if task == nil || g.wakeRunning[id] {
		g.wakeMu.Unlock()
		return
	}
	if reason == "schedule" && !task.Enabled {
		g.wakeMu.Unlock()
		return
	}
	select {
	case g.wakeSem <- struct{}{}:
	default:
		g.wakeMu.Unlock()
		return
	}
	g.wakeRunning[id] = true
	accountID, model, maxTokens := task.AccountID, task.Model, task.MaxTokens
	label := task.Label
	g.wakeMu.Unlock()

	ok, msg, usedModel, _ := g.wakeCall(accountID, model, maxTokens, wakeRunTimeout)

	<-g.wakeSem
	now := time.Now().Unix()
	g.wakeMu.Lock()
	if t := g.findWakeLocked(id); t != nil {
		t.LastRun = now
		t.LastOK = ok
		t.LastModel = usedModel
		if ok {
			t.Failures = 0
			t.LastResult = ""
		} else {
			t.Failures++
			t.LastResult = truncateWake(msg, 300)
		}
	}
	delete(g.wakeRunning, id)
	g.saveWakeLocked()
	g.wakeMu.Unlock()
	_ = label
}

func (g *Gateway) findWakeLocked(id string) *wakeTask {
	for _, t := range g.wakeTasks {
		if t != nil && t.ID == id {
			return t
		}
	}
	return nil
}

// wakeCall 对指定账号发一个最小请求。返回是否拿到首个事件、说明、最终模型与耗时。
func (g *Gateway) wakeCall(accountID, model string, maxTokens int, timeout time.Duration) (ok bool, msg, usedModel string, ms int64) {
	var env *provider.AccountEnv
	for _, e := range g.pool.All() {
		if e.AccountID == accountID {
			env = e
			break
		}
	}
	if env == nil {
		return false, "账号不存在（可能已被删除）", "", 0
	}
	if !env.Available() {
		return false, unavailableReason(env), "", 0
	}
	p, found := provider.Get(env.ProviderID)
	if !found {
		return false, "渠道未注册: " + env.ProviderID, "", 0
	}
	chat, canChat := provider.AsChat(p)
	if !canChat {
		return false, "该渠道不支持对话，无法唤醒", "", 0
	}
	usedModel = strings.TrimSpace(model)
	if usedModel == "" {
		for _, m := range p.Manifest().Models {
			if strings.TrimSpace(m.UpstreamModel) != "" {
				usedModel = m.UpstreamModel
				break
			}
		}
	}
	if usedModel == "" {
		return false, "渠道没有登记模型，无法唤醒", "", 0
	}
	if maxTokens <= 0 {
		maxTokens = wakeDefaultTokens
	}
	req := &ir.UnifiedChatRequest{
		Model:              usedModel,
		UpstreamModel:      usedModel,
		ProviderID:         env.ProviderID,
		DownstreamProtocol: ir.ProtocolOpenAIChat,
		Stream:             true,
		Items:              []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "ping"}},
		MaxOutputTokens:    &maxTokens,
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	start := time.Now()
	ch, err := chat.ChatStream(ctx, env, req)
	if err != nil {
		// 唤醒走的是渠道直连（不经过 dispatch 的罚分逻辑），所以失败必须在这里
		// 自己标记账号状态：否则"额度耗尽"只会弹一条 toast，账号仍显示 ready，
		// 下一次路由照样选它、再撞一次墙。
		if ae := ir.AsAPIError(err); ae != nil {
			g.penalizeModel(env, ae, usedModel)
			return false, wakeFailText(ae), usedModel, time.Since(start).Milliseconds()
		}
		return false, err.Error(), usedModel, time.Since(start).Milliseconds()
	}
	// 首个有效事件即视为唤醒成功；之后继续把流读完，避免上游 goroutine 阻塞在缓冲通道上。
	sawEvent := false
	var firstErr string
	var firstErrAPI *ir.APIError
	for {
		select {
		case <-ctx.Done():
			if sawEvent {
				return true, "", usedModel, time.Since(start).Milliseconds()
			}
			return false, "超时：没有在时限内收到回应", usedModel, time.Since(start).Milliseconds()
		case ev, open := <-ch:
			if !open {
				if sawEvent {
					return true, "", usedModel, time.Since(start).Milliseconds()
				}
				if firstErr != "" {
					// 流内错误同样按额度/限流语义处理一次，保证状态与真实可用性一致。
					if firstErrAPI != nil {
						g.penalizeModel(env, firstErrAPI, usedModel)
						firstErr = wakeFailText(firstErrAPI)
					}
					return false, firstErr, usedModel, time.Since(start).Milliseconds()
				}
				return true, "", usedModel, time.Since(start).Milliseconds()
			}
			switch ev.Type {
			case ir.EventError:
				if firstErr == "" {
					if ae := ir.AsAPIError(ev.Err); ae != nil {
						firstErrAPI = ae
						firstErr = wakeFailText(ae)
					} else if ev.Err != nil {
						firstErr = ev.Err.Error()
					} else {
						firstErr = "上游返回错误"
					}
				}
			case ir.EventTextDelta, ir.EventReasoningDelta, ir.EventToolCallStart, ir.EventToolCallDelta:
				sawEvent = true
			case ir.EventUsage, ir.EventDone:
				sawEvent = true
			}
		}
	}
}

// --- HTTP API ---

// handleWakeList GET /api/wake
func (g *Gateway) handleWakeList(w http.ResponseWriter, r *http.Request) {
	g.wakeMu.Lock()
	views := make([]wakeTaskView, 0, len(g.wakeTasks))
	for _, t := range g.wakeTasks {
		if t == nil {
			continue
		}
		views = append(views, wakeTaskView{
			wakeTask: *t,
			NextRun:  t.nextRunAt().Unix(),
			Running:  g.wakeRunning[t.ID],
		})
	}
	g.wakeMu.Unlock()
	sort.Slice(views, func(i, j int) bool { return views[i].CreatedAt < views[j].CreatedAt })
	writeJSON(w, http.StatusOK, map[string]any{"tasks": views, "now": time.Now().Unix()})
}

// handleWakeSave POST /api/wake
//
// 请求体：{"account_id":"...","model":"","every_minutes":300,"max_tokens":8,"enabled":true}
// 同一账号重复提交视为更新（保留历史执行记录）。
func (g *Gateway) handleWakeSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID    string `json:"account_id"`
		Model        string `json:"model"`
		EveryMinutes int    `json:"every_minutes"`
		MaxTokens    int    `json:"max_tokens"`
		Enabled      *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	accountID := strings.TrimSpace(body.AccountID)
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 account_id"})
		return
	}
	var env *provider.AccountEnv
	for _, e := range g.pool.All() {
		if e.AccountID == accountID {
			env = e
			break
		}
	}
	if env == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "账号不存在"})
		return
	}
	p, found := provider.Get(env.ProviderID)
	if !found {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "渠道未注册: " + env.ProviderID})
		return
	}
	if _, canChat := provider.AsChat(p); !canChat {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "该渠道不支持对话，无法创建唤醒任务"})
		return
	}

	every := body.EveryMinutes
	if every == 0 {
		every = wakeDefaultEvery
	}
	if errMsg := validateWakeInterval(every); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": errMsg})
		return
	}
	maxTokens := body.MaxTokens
	if maxTokens <= 0 {
		maxTokens = wakeDefaultTokens
	}
	if maxTokens > 64 {
		maxTokens = 64
	}

	g.wakeMu.Lock()
	task := g.findWakeByAccountLocked(accountID)
	if task == nil {
		task = &wakeTask{ID: wakeID(), AccountID: accountID, CreatedAt: time.Now().Unix(), Enabled: true}
		g.wakeTasks = append(g.wakeTasks, task)
	}
	task.ProviderID = env.ProviderID
	task.Label = env.Label
	task.Model = strings.TrimSpace(body.Model)
	task.EveryMinutes = every
	task.MaxTokens = maxTokens
	if body.Enabled != nil {
		task.Enabled = *body.Enabled
	}
	g.saveWakeLocked()
	view := wakeTaskView{wakeTask: *task, NextRun: task.nextRunAt().Unix(), Running: g.wakeRunning[task.ID]}
	g.wakeMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"task": view})
}

// handleWakeUpdate POST /api/wake/{id}
func (g *Gateway) handleWakeUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Model        *string `json:"model"`
		EveryMinutes *int    `json:"every_minutes"`
		MaxTokens    *int    `json:"max_tokens"`
		Enabled      *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	g.wakeMu.Lock()
	task := g.findWakeLocked(id)
	if task == nil {
		g.wakeMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "任务不存在"})
		return
	}
	if body.Model != nil {
		task.Model = strings.TrimSpace(*body.Model)
	}
	if body.EveryMinutes != nil {
		if errMsg := validateWakeInterval(*body.EveryMinutes); errMsg != "" {
			g.wakeMu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": errMsg})
			return
		}
		task.EveryMinutes = *body.EveryMinutes
	}
	if body.MaxTokens != nil {
		n := *body.MaxTokens
		if n <= 0 {
			n = wakeDefaultTokens
		}
		if n > 64 {
			n = 64
		}
		task.MaxTokens = n
	}
	if body.Enabled != nil {
		task.Enabled = *body.Enabled
	}
	g.saveWakeLocked()
	view := wakeTaskView{wakeTask: *task, NextRun: task.nextRunAt().Unix(), Running: g.wakeRunning[task.ID]}
	g.wakeMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"task": view})
}

// handleWakeDelete DELETE /api/wake/{id}
func (g *Gateway) handleWakeDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g.wakeMu.Lock()
	kept := g.wakeTasks[:0]
	removed := false
	for _, t := range g.wakeTasks {
		if t != nil && t.ID == id {
			removed = true
			continue
		}
		kept = append(kept, t)
	}
	g.wakeTasks = kept
	if removed {
		delete(g.wakeRunning, id)
		g.saveWakeLocked()
	}
	g.wakeMu.Unlock()
	if !removed {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "任务不存在"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// handleWakeRunTask POST /api/wake/{id}/run  异步执行一条任务。
func (g *Gateway) handleWakeRunTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g.wakeMu.Lock()
	task := g.findWakeLocked(id)
	if task == nil {
		g.wakeMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "任务不存在"})
		return
	}
	if g.wakeRunning[id] {
		g.wakeMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"running": true})
		return
	}
	g.wakeMu.Unlock()
	go g.runWakeTask(id, "manual")
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

// handleWakeRunAccount POST /api/wake/run
//
// 一次性唤醒（不建任务）：{"account_id":"...","model":"","max_tokens":8}
func (g *Gateway) handleWakeRunAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID string `json:"account_id"`
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	accountID := strings.TrimSpace(body.AccountID)
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 account_id"})
		return
	}
	select {
	case g.wakeSem <- struct{}{}:
		defer func() { <-g.wakeSem }()
	default:
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "唤醒请求过多，请稍后再试"})
		return
	}
	ok, msg, usedModel, ms := g.wakeCall(accountID, body.Model, body.MaxTokens, wakeRunTimeout)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": ok, "result": msg, "model": usedModel, "ms": ms, "account_id": accountID,
	})
}

func (g *Gateway) findWakeByAccountLocked(accountID string) *wakeTask {
	for _, t := range g.wakeTasks {
		if t != nil && t.AccountID == accountID {
			return t
		}
	}
	return nil
}

func validateWakeInterval(minutes int) string {
	d := time.Duration(minutes) * time.Minute
	if d < wakeMinInterval || d > wakeMaxInterval {
		return "间隔需在 15 分钟到 7 天之间"
	}
	return ""
}

func wakeID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "wake-" + time.Now().Format("150405.000000")
	}
	return "wake-" + hex.EncodeToString(b[:])
}

func truncateWake(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// wakeFailText 把上游错误翻译成"用户能据此行动"的一句话。
//
// 直接抛 err.Error() 会出现 `upstream_rate_limited: Qoder 账号额度已用尽（上游 400：
// {"code":"400","message":"[FAIL]node:agent_router ..."}）` 这种把协议细节糊在脸上的
// 文案：用户既不知道是不是自己配错了，也不知道该等还是该换账号。
func wakeFailText(ae *ir.APIError) string {
	if ae == nil {
		return "未知错误"
	}
	if quotaExhaustedHint(ae.Message) {
		return "账号额度已用尽，唤醒不会产生流量；充值或换账号后再试（原始信息：" + brief(ae.Message, 120) + "）"
	}
	switch ae.Code {
	case ir.ErrCodeUpstreamRateLimit:
		return "上游限流，稍后自动重试（" + brief(ae.Message, 120) + "）"
	case ir.ErrCodeUpstreamAuth:
		return "登录态失效，请重新导入或粘贴 Token（" + brief(ae.Message, 120) + "）"
	case ir.ErrCodeUpstreamFailed:
		return "上游不可达或返回异常（" + brief(ae.Message, 120) + "）"
	}
	return ae.Message
}

// brief 截断长文案（保留头部，超出补省略号）。
func brief(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if n > 0 && len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// unavailableReason 把"账号不可用"说清楚：具体状态、还要等多久、为什么。
//
// 原来只报"冷却/停用/失效"三个词，用户看到的第一反应是"我明明还有额度"——
// 因为额度确实还在，真正的原因可能是连续失败触发的熔断，或者是凭证失效。
// 报错必须能直接指向该做的动作。
func unavailableReason(env *provider.AccountEnv) string {
	state, msg, resetAt := env.StateDetail()
	var left time.Duration
	if !resetAt.IsZero() {
		left = time.Until(resetAt)
	}
	switch state {
	case provider.StateCoolingDown:
		if msg != "" {
			if left > 0 {
				return fmt.Sprintf("账号正在冷却（%s，还剩 %s）", msg, left.Round(time.Second))
			}
			return "账号正在冷却：" + msg
		}
		if left > 0 {
			return fmt.Sprintf("账号正在冷却，还剩 %s", left.Round(time.Second))
		}
		return "账号正在冷却"
	case provider.StateDepleted:
		if left > 0 {
			return fmt.Sprintf("账号额度已用尽，%s 后恢复", left.Round(time.Second))
		}
		return "账号额度已用尽"
	case provider.StateInvalid:
		if msg != "" {
			return "账号凭证已失效，需要重新登录：" + msg
		}
		return "账号凭证已失效，需要重新登录"
	case provider.StateRefreshing:
		return "账号正在刷新凭证，请稍后重试"
	}
	if msg != "" {
		return "账号当前不可用：" + msg
	}
	return "账号当前不可用（可能已被停用）"
}
