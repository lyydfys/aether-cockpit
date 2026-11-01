package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 用量统计 —— 对齐 aether 的 usage/cost 面板。
//
// 每个 IR 路径请求在流结束时聚合成一个格子：
// date × provider × model × account -> 请求数 / token / 耗时。
// 按天落盘到 data/usage.json（保留 90 天），成本按设置里的价格表在
// 查询时计算——价格会变，所以不把成本写死在格子里。
//
// Fast-Path 零损直通的流不经过 IR，拿不到 usage 事件；
// 这类请求只记请求数，token 记为 0（宁可缺数，不破坏零损保证）。

// usageMaxDays 聚合数据保留天数。
const usageMaxDays = 90

// usageCell 是聚合的最小格子。
type usageCell struct {
	Date       string `json:"date"`
	ProviderID string `json:"provider_id,omitempty"`
	Model      string `json:"model,omitempty"`
	AccountID  string `json:"account_id,omitempty"`
	// CallerKey 是调用方网关密钥（空 = 本机未带密钥）。
	CallerKey string `json:"caller_key,omitempty"`
	// Client 是调用方 agent 的识别结果（Claude Code / Codex / curl…）。
	// 用量页的「按 Agent」列表读它；老数据没有该字段时为空，仍会计入总量。
	Client          string `json:"client,omitempty"`
	Requests        int64  `json:"requests"`
	Errors          int64  `json:"errors"`
	InputTokens     int64  `json:"input_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	CacheCreate     int64  `json:"cache_creation_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	DurationMS      int64  `json:"duration_ms"`
	FirstByteMS     int64  `json:"first_byte_ms"`
}

func (c *usageCell) addTokens(u *ir.Usage) {
	if u == nil {
		return
	}
	in := int64(u.InputTokens)
	out := int64(u.OutputTokens)
	// 归一后的 TotalTokens 应等于各类输入之和 + 输出；某些渠道仍按旧口径
	// 上报（total 包含一切），多出来的部分补进普通输入，避免总量丢失。
	sum := int64(u.InputTokens + u.CachedTokens + u.CacheCreationTokens + u.OutputTokens)
	if u.TotalTokens > int(sum) {
		in += int64(u.TotalTokens) - sum
	}
	c.InputTokens += in
	c.OutputTokens += out
	c.CachedTokens += int64(u.CachedTokens)
	c.CacheCreate += int64(u.CacheCreationTokens)
	c.ReasoningTokens += int64(u.ReasoningTokens)
}

// usageStore 是按天组织的聚合存储。
type usageStore struct {
	mu      sync.Mutex
	path    string
	days    map[string]map[string]*usageCell // date -> "provider|model|account" -> cell
	maxDays int
}

func newUsageStore(path string) *usageStore {
	s := &usageStore{
		path:    path,
		days:    make(map[string]map[string]*usageCell, 32),
		maxDays: usageMaxDays,
	}
	s.load()
	return s
}

func usageDate(t time.Time) string {
	return t.Format("2006-01-02")
}

func cellKey(providerID, model, accountID, client, callerKey string) string {
	return providerID + "|" + model + "|" + accountID + "|" + client + "|" + callerKey
}

// record 累加一个格子。零值格子不落盘。
func (s *usageStore) record(cell usageCell) {
	if cell.Requests == 0 && cell.InputTokens == 0 && cell.OutputTokens == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	day, ok := s.days[cell.Date]
	if !ok {
		day = make(map[string]*usageCell, 32)
		s.days[cell.Date] = day
	}
	key := cellKey(cell.ProviderID, cell.Model, cell.AccountID, cell.Client, cell.CallerKey)
	c, ok := day[key]
	if !ok {
		cp := cell
		c = &cp
		day[key] = c
		return
	}
	c.Requests += cell.Requests
	c.Errors += cell.Errors
	c.InputTokens += cell.InputTokens
	c.OutputTokens += cell.OutputTokens
	c.CachedTokens += cell.CachedTokens
	c.ReasoningTokens += cell.ReasoningTokens
	c.DurationMS += cell.DurationMS
	c.FirstByteMS += cell.FirstByteMS
}

// load 启动时恢复。损坏文件按空处理。
func (s *usageStore) load() {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var f struct {
		Days map[string]map[string]*usageCell `json:"days"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return
	}
	if f.Days != nil {
		s.days = f.Days
	}
	s.pruneLocked()
}

// save 原子写盘。调用方持有 mu。
func (s *usageStore) save() error {
	if dir := filepath.Dir(s.path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.pruneLocked()
	f := struct {
		Version int                              `json:"version"`
		SavedAt int64                            `json:"saved_at"`
		Days    map[string]map[string]*usageCell `json:"days"`
	}{Version: 1, SavedAt: time.Now().Unix(), Days: s.days}
	b, err := json.MarshalIndent(f, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *usageStore) pruneLocked() {
	if len(s.days) <= s.maxDays {
		return
	}
	dates := make([]string, 0, len(s.days))
	for d := range s.days {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	for _, d := range dates[:len(dates)-s.maxDays] {
		delete(s.days, d)
	}
}

// flush 落盘（有变化才写）。
func (s *usageStore) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.save()
}

// StartAutoSave 周期落盘，返回停止函数（停止时做最后一次 flush）。
func (s *usageStore) StartAutoSave(interval time.Duration) func() {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				s.flush()
				return
			case <-t.C:
				s.flush()
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}

// usageDayQuery 是查询结果里"一天"的聚合视图。
type usageDayQuery struct {
	Date         string                  `json:"date"`
	Requests     int64                   `json:"requests"`
	Errors       int64                   `json:"errors"`
	InputTokens  int64                   `json:"input_tokens"`
	OutputTokens int64                   `json:"output_tokens"`
	CachedTokens int64                   `json:"cached_tokens"`
	CacheCreate  int64                   `json:"cache_creation_tokens"`
	Reasoning    int64                   `json:"reasoning_tokens"`
	Cost         float64                 `json:"cost"`
	ByProvider   map[string]*usageTotals `json:"by_provider"`
}

type usageTotals struct {
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CachedTokens int64   `json:"cached_tokens"`
	CacheCreate  int64   `json:"cache_creation_tokens"`
	Reasoning    int64   `json:"reasoning_tokens"`
	Cost         float64 `json:"cost"`
}

type usageModelRow struct {
	Model        string  `json:"model"`
	Requests     int64   `json:"requests"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CachedTokens int64   `json:"cached_tokens"`
	CacheCreate  int64   `json:"cache_creation_tokens"`
	Reasoning    int64   `json:"reasoning_tokens"`
	Cost         float64 `json:"cost"`
}

// priceFor 按模型名查价格：精确 -> 最长前缀 -> "*" 兜底。
func (g *Gateway) priceFor(model string) (ModelPrice, bool) {
	g.settingsMu.Lock()
	prices := g.settings.ModelPrices
	g.settingsMu.Unlock()
	if p, ok := matchPriceMap(prices, model); ok {
		return p, true
	}
	bare := bareModelName(model)
	if bare != model {
		if p, ok := matchPriceMap(prices, bare); ok {
			return p, true
		}
	}
	return defaultPriceFor(bare)
}

// accumulateUsage 把一格累加进任意汇总维度（按渠道 / 按模型 / 按账号）。
func accumulateUsage(dst *usageTotals, c *usageCell, cost float64) {
	dst.Requests += c.Requests
	dst.Errors += c.Errors
	dst.InputTokens += c.InputTokens
	dst.OutputTokens += c.OutputTokens
	dst.CachedTokens += c.CachedTokens
	dst.CacheCreate += c.CacheCreate
	dst.Reasoning += c.ReasoningTokens
	dst.Cost += cost
}

func costOf(p ModelPrice, in, out, cacheRead, cacheWrite int64) float64 {
	readRate := p.CachedReadPerM
	if readRate == 0 {
		readRate = p.InputPerM
	}
	writeRate := p.CachedWritePerM
	if writeRate == 0 {
		writeRate = p.InputPerM
	}
	return float64(in)/1e6*p.InputPerM +
		float64(out)/1e6*p.OutputPerM +
		float64(cacheRead)/1e6*readRate +
		float64(cacheWrite)/1e6*writeRate
}

// clientAgent 把一次请求归到调用方 agent。
//
// 顺序很重要：先认 UA 里的客户端自报名（Claude Code / Codex / Gemini CLI
// 都会带），认不出来时才用协议路径兜底——否则所有自定义 UA 都会挤进
// 「anthropic 协议」这一类，Agent 列表就失去意义。
func clientAgent(r *http.Request) string {
	ua := strings.ToLower(r.UserAgent())
	switch {
	case strings.Contains(ua, "claude-cli"), strings.Contains(ua, "claude-code"):
		return "claude-code"
	case strings.Contains(ua, "codex"):
		return "codex"
	case strings.Contains(ua, "gemini-cli"):
		return "gemini-cli"
	case strings.Contains(ua, "opencode"):
		return "opencode"
	case strings.Contains(ua, "cursor"):
		return "cursor"
	case strings.Contains(ua, "kiro"):
		return "kiro"
	case strings.Contains(ua, "curl") || strings.Contains(ua, "wget"):
		return "curl"
	case strings.Contains(ua, "python"), strings.Contains(ua, "httpx"), strings.Contains(ua, "aiohttp"):
		return "python"
	case strings.Contains(ua, "node"), strings.Contains(ua, "undici"), strings.Contains(ua, "axios"), strings.Contains(ua, "electron"):
		return "node"
	case strings.Contains(ua, "go-http-client"):
		return "go"
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1/messages"):
		return "anthropic"
	case strings.HasPrefix(r.URL.Path, "/v1/responses"):
		return "openai-responses"
	case strings.HasPrefix(r.URL.Path, "/v1beta"):
		return "gemini"
	case strings.HasPrefix(r.URL.Path, "/v1/chat/completions"):
		return "openai-chat"
	}
	return "other"
}

// maskAgentID 把 UA 分类映射到本机 Agent 的 ID（脱敏页用 agent 的图标/名称）。
func maskAgentID(id string) string {
	switch id {
	case "claude-code":
		return "claude_code"
	case "codex":
		return "codex_cli"
	case "gemini-cli":
		return "gemini_cli"
	}
	return id
}

// clientAgentNames 是识别结果的展示名。
var clientAgentNames = map[string]string{
	"claude-code":      "Claude Code",
	"codex":            "Codex",
	"gemini-cli":       "Gemini CLI",
	"opencode":         "OpenCode",
	"cursor":           "Cursor",
	"kiro":             "Kiro",
	"curl":             "curl",
	"python":           "Python",
	"node":             "Node.js",
	"go":               "Go",
	"anthropic":        "Anthropic 协议",
	"openai-responses": "OpenAI Responses",
	"openai-chat":      "OpenAI Chat",
	"gemini":           "Gemini 协议",
	"other":            "其他客户端",
}

func clientAgentName(id string) string {
	if id == "" {
		return "未记录客户端"
	}
	if n, ok := clientAgentNames[id]; ok {
		return n
	}
	return id
}

// handleUsage GET /api/usage?days=14
func (g *Gateway) handleUsage(w http.ResponseWriter, r *http.Request) {
	days := 14
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= usageMaxDays {
			days = n
		}
	}
	// 前端用 period 表达时间窗（today / 7d / 30d / all / from..to），
	// 与 days 等价，这里统一折算成天数。
	if p := r.URL.Query().Get("period"); p != "" {
		switch p {
		case "today":
			days = 1
		case "7d":
			days = 7
		case "30d":
			days = 30
		case "all", "":
			days = usageMaxDays
		default:
			if from, to, ok := strings.Cut(p, ".."); ok {
				if a, err1 := time.Parse("2006-01-02", from); err1 == nil {
					if b, err2 := time.Parse("2006-01-02", to); err2 == nil {
						if n := int(b.Sub(a).Hours()/24) + 1; n > 0 && n <= usageMaxDays {
							days = n
						}
					}
				}
			}
		}
	}

	s := g.usage
	if s == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"agents": []any{}, "models": []any{}, "accounts": []any{},
			"callerKeys": []any{}, "series": []any{}, "bucket": "day",
			"calls": 0, "errors": 0, "input": 0, "output": 0,
			"cache_read": 0, "cache_write": 0, "reasoning": 0, "cost": 0,
			"unpriced": 0, "retried": 0, "path": "",
		})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// 只取最近 N 天（含今天）。
	today := time.Now()
	dates := make([]string, 0, days)
	for i := days - 1; i >= 0; i-- {
		dates = append(dates, usageDate(today.AddDate(0, 0, -i)))
	}

	perDay := make([]usageDayQuery, 0, len(dates))
	series := make([]map[string]any, 0, len(dates))
	models := make(map[string]*usageTotals, 16)
	accounts := make(map[string]*usageTotals, 8)
	clients := make(map[string]*usageTotals, 8)
	callerKeys := make(map[string]*usageTotals, 8)
	accountProvider := make(map[string]string, 8)
	var total usageTotals
	var unpriced int64

	for _, date := range dates {
		q := usageDayQuery{Date: date, ByProvider: make(map[string]*usageTotals, 8)}
		for _, c := range s.days[date] {
			q.Requests += c.Requests
			q.Errors += c.Errors
			q.InputTokens += c.InputTokens
			q.OutputTokens += c.OutputTokens
			q.CachedTokens += c.CachedTokens
			q.CacheCreate += c.CacheCreate
			q.Reasoning += c.ReasoningTokens

			price, hasPrice := g.priceForUsage(c.Model, c.InputTokens+c.CachedTokens+c.CacheCreate, time.Now())
			cost := 0.0
			if hasPrice {
				cost = costOf(price, c.InputTokens, c.OutputTokens, c.CachedTokens, c.CacheCreate)
				// 渠道计费倍率（高级 → Price rate）：0.8 打折、1.5 加价。
				if rate := g.providerPriceRate(c.ProviderID); rate != 1 {
					cost *= rate
				}
			} else if c.Requests > 0 {
				unpriced += c.Requests
			}
			q.Cost += cost

			get := func(m map[string]*usageTotals, k string) *usageTotals {
				t, ok := m[k]
				if !ok {
					t = &usageTotals{}
					m[k] = t
				}
				return t
			}
			accumulateUsage(get(q.ByProvider, c.ProviderID), c, cost)
			accumulateUsage(get(models, c.Model), c, cost)
			accumulateUsage(get(accounts, c.AccountID), c, cost)
			client := c.Client
			if client == "" {
				client = "other"
			}
			accumulateUsage(get(clients, client), c, cost)
			if c.CallerKey != "" {
				accumulateUsage(get(callerKeys, c.CallerKey), c, cost)
			}
			if c.AccountID != "" && c.ProviderID != "" {
				accountProvider[c.AccountID] = c.ProviderID
			}
		}
		total.Requests += q.Requests
		total.Errors += q.Errors
		total.InputTokens += q.InputTokens
		total.OutputTokens += q.OutputTokens
		total.CachedTokens += q.CachedTokens
		total.CacheCreate += q.CacheCreate
		total.Reasoning += q.Reasoning
		total.Cost += q.Cost
		perDay = append(perDay, q)
		// 图表的一天：time 给 RFC3339，让前端按读者语言本地化日期。
		t, _ := time.ParseInLocation("2006-01-02", date, time.Local)
		series = append(series, map[string]any{
			"label": t.Format("Jan 2"), "time": t.Format(time.RFC3339),
			"calls": q.Requests, "errors": q.Errors,
			"input": q.InputTokens, "output": q.OutputTokens,
			"cache_read": q.CachedTokens, "cache_write": q.CacheCreate,
			"reasoning": q.Reasoning, "cost": q.Cost,
		})
	}

	modelRows := make([]usageModelRow, 0, len(models))
	for m, t := range models {
		modelRows = append(modelRows, usageModelRow{
			Model: m, Requests: t.Requests,
			InputTokens: t.InputTokens, OutputTokens: t.OutputTokens,
			CachedTokens: t.CachedTokens, CacheCreate: t.CacheCreate,
			Reasoning: t.Reasoning, Cost: t.Cost,
		})
	}
	sort.Slice(modelRows, func(i, j int) bool { return modelRows[i].Cost > modelRows[j].Cost })

	// 用量页的「按 Agent」「按模型」「按账号」三张列表读同一套行结构：
	// {id, name, calls, errors, input, output, cache_read, cache_write, cost}
	// Agent 是调用方客户端（UA/协议识别），账号行额外带渠道名做副标题。
	agentRows := make([]map[string]any, 0, len(clients))
	for id, t := range clients {
		agentRows = append(agentRows, map[string]any{
			"id": id, "name": clientAgentName(id), "icon": "generic",
			"calls": int(t.Requests), "errors": int(t.Errors),
			"input": int(t.InputTokens), "output": int(t.OutputTokens),
			"cache_read": int(t.CachedTokens), "cache_write": int(t.CacheCreate),
			"reasoning": int(t.Reasoning), "cost": t.Cost,
		})
	}
	sort.Slice(agentRows, func(i, j int) bool {
		ti := agentRows[i]["input"].(int) + agentRows[i]["output"].(int)
		tj := agentRows[j]["input"].(int) + agentRows[j]["output"].(int)
		if ti != tj {
			return ti > tj
		}
		return agentRows[i]["calls"].(int) > agentRows[j]["calls"].(int)
	})

	accountRows := make([]map[string]any, 0, len(accounts))
	for id, t := range accounts {
		name := id
		if id == "" {
			name = "未记录账号"
		}
		if g.pool != nil {
			if env, ok := g.pool.Get(id); ok && env.Label != "" {
				name = env.Label
			}
		}
		providerID := accountProvider[id]
		providerName := providerID
		if p, ok := provider.Get(providerID); ok {
			providerName = p.Manifest().DisplayName
		}
		accountRows = append(accountRows, map[string]any{
			"id": id, "name": name, "provider": providerID, "sub": providerName,
			"icon":  "generic",
			"calls": int(t.Requests), "errors": int(t.Errors),
			"input": int(t.InputTokens), "output": int(t.OutputTokens),
			"cache_read": int(t.CachedTokens), "cache_write": int(t.CacheCreate),
			"reasoning": int(t.Reasoning), "cost": t.Cost,
		})
	}
	sort.Slice(accountRows, func(i, j int) bool {
		return accountRows[i]["calls"].(int) > accountRows[j]["calls"].(int)
	})

	callerKeyRows := make([]map[string]any, 0, len(callerKeys))
	for id, t := range callerKeys {
		name := id
		if g.callerKeys != nil {
			if k := g.callerKeys.get(id); k != nil && k.Name != "" {
				name = k.Name
			}
		}
		callerKeyRows = append(callerKeyRows, map[string]any{
			"id": id, "name": name, "icon": "key",
			"calls": int(t.Requests), "errors": int(t.Errors),
			"input": int(t.InputTokens), "output": int(t.OutputTokens),
			"cache_read": int(t.CachedTokens), "cache_write": int(t.CacheCreate),
			"reasoning": int(t.Reasoning), "cost": t.Cost,
		})
	}
	sort.Slice(callerKeyRows, func(i, j int) bool {
		return callerKeyRows[i]["calls"].(int) > callerKeyRows[j]["calls"].(int)
	})

	modelViews := make([]map[string]any, 0, len(modelRows))
	for _, m := range modelRows {
		modelViews = append(modelViews, map[string]any{
			"id": m.Model, "name": m.Model, "icon": "generic",
			"calls": int(m.Requests), "errors": 0,
			"input": int(m.InputTokens), "output": int(m.OutputTokens),
			"cache_read": int(m.CachedTokens), "cache_write": int(m.CacheCreate),
			"reasoning": int(m.Reasoning), "cost": m.Cost,
		})
	}

	// 顶层字段同时满足两套命名：
	//   totals/days/models_native 是内核原生的聚合结果；
	//   calls/input/output/cost/series/agents/models/accounts 是前端用量页直接读的。
	// 用量页只认后者，缺了它就只会显示空态。
	writeJSON(w, http.StatusOK, map[string]any{
		"days":            perDay,
		"totals":          total,
		"models_native":   modelRows,
		"accounts_native": accounts,
		"retention":       usageMaxDays,

		"period":      r.URL.Query().Get("period"),
		"calls":       total.Requests,
		"errors":      total.Errors,
		"input":       total.InputTokens,
		"output":      total.OutputTokens,
		"cache_read":  total.CachedTokens,
		"cache_write": total.CacheCreate,
		"reasoning":   total.Reasoning,
		"cost":        total.Cost,
		"unpriced":    unpriced,
		"retried":     0,
		"callerKeys":  callerKeyRows,
		"agents":      agentRows,
		"models":      modelViews,
		"accounts":    accountRows,
		"series":      series,
		"bucket":      "day",
		"path":        s.path,
	})
}

// streamDrainGrace 是客户端断流后继续读上游的上限。
// 它换来的是"中断的请求仍然计入用量"，代价是最多让上游多生成这么久。
const streamDrainGrace = 20 * time.Second

// withUsageRecorder 包装上游事件流，统计本次请求的 token 用量，
// 在流结束时写入聚合存储，并把时延观测回灌给账号（fast 路由策略的数据源）。
// 只在 IR 路径上使用。
func (g *Gateway) withUsageRecorder(ctx context.Context, ch <-chan ir.UnifiedStreamEvent, base usageCell, env *provider.AccountEnv, sessionKey string, led *ledgerPending) <-chan ir.UnifiedStreamEvent {
	if g.usage == nil && env == nil {
		return ch
	}
	out := make(chan ir.UnifiedStreamEvent, 8)
	go func() {
		defer close(out)
		start := time.Now()
		var usage *ir.Usage
		sawError := false
		var firstByteMS int64

		finish := func() {
			base.Requests = 1
			if sawError {
				base.Errors = 1
			}
			if usage != nil {
				base.addTokens(usage)
			}
			base.FirstByteMS = firstByteMS
			base.DurationMS = time.Since(start).Milliseconds()
			if g.usage != nil {
				g.usage.record(base)
			}
			if env != nil {
				env.NoteLatency(firstByteMS, base.OutputTokens, base.DurationMS)
			}
			if led != nil {
				led.noteUsage(usage, firstByteMS, base.DurationMS)
			}
			// 把本轮的 prompt-cache 读写量回写给粘性绑定：
			// 缓存量大就多保一会儿亲和，量小就让它尽早失效。
			if g.pool != nil && sessionKey != "" && usage != nil {
				g.pool.NoteStickyCache(sessionKey, usage.CachedTokens+usage.CacheCreationTokens)
			}
		}
		absorb := func(ev ir.UnifiedStreamEvent) {
			// 首字节口径：第一个"用户看得见"的增量，而不是 start 事件。
			if firstByteMS == 0 {
				switch ev.Type {
				case ir.EventTextDelta, ir.EventReasoningDelta, ir.EventToolCallStart:
					firstByteMS = time.Since(start).Milliseconds()
				}
			}
			switch ev.Type {
			case ir.EventUsage:
				if ev.Usage != nil {
					usage = ev.Usage
				}
			case ir.EventError:
				sawError = true
			}
		}

		// drain 在客户端断流后继续把上游读完（不再下发），只为拿到末尾的 usage；
		// 超过宽限期仍没读完就放弃——不能让一个断开的客户端拖住上游无限期。
		drain := func() {
			deadline := time.NewTimer(streamDrainGrace + 5*time.Second)
			defer deadline.Stop()
			for {
				select {
				case ev, ok := <-ch:
					if !ok {
						finish()
						return
					}
					absorb(ev)
				case <-deadline.C:
					finish()
					return
				}
			}
		}

		for {
			select {
			case <-ctx.Done():
				drain()
				return
			case ev, ok := <-ch:
				if !ok {
					finish()
					return
				}
				absorb(ev)
				select {
				case out <- ev:
				case <-ctx.Done():
					drain()
					return
				}
			}
		}
	}()
	return out
}
