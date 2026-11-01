package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 渠道监控：手动触发一次最小对话探测（不会自动定时 ping，避免悄悄消耗
// 真实账号额度），结果按账号保留最近一次 + 20 条历史，供网关页卡片展示。

type channelProbe struct {
	ProviderID string        `json:"provider"`
	AccountID  string        `json:"account"`
	Label      string        `json:"label,omitempty"`
	Model      string        `json:"model,omitempty"`
	OK         bool          `json:"ok"`
	Code       int           `json:"code,omitempty"` // 探测到的 HTTP 状态码：成功 200，失败从错误文本里取 429/502 等
	Ms         int64         `json:"ms"`
	At         int64         `json:"at"`
	Error      string        `json:"error,omitempty"`
	History    []probeSample `json:"history,omitempty"`
}

type probeSample struct {
	At    int64  `json:"at"`
	OK    bool   `json:"ok"`
	Ms    int64  `json:"ms"`
	Error string `json:"error,omitempty"`
}

const (
	probeHistoryLimit = 20
	probeTimeout      = 20 * time.Second
)

// monitorInterval 返回自动探测间隔；0 表示关闭，最小 5 分钟。
func (g *Gateway) monitorInterval() time.Duration {
	g.settingsMu.Lock()
	n := g.settings.MonitorMinutes
	g.settingsMu.Unlock()
	if n <= 0 {
		return 0
	}
	if n < 5 {
		n = 5
	}
	if n > 1440 {
		n = 1440
	}
	return time.Duration(n) * time.Minute
}

// monitorLoop 是自动探测的节拍器（30 秒看一次设置是否到期）。
// 只有用户在设置里显式开启才会真正探测。
func (g *Gateway) monitorLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		interval := g.monitorInterval()
		if interval == 0 {
			continue
		}
		g.monitorMu.Lock()
		due := !g.monitorReading && (g.monitorAt == 0 || time.Since(time.Unix(g.monitorAt, 0)) >= interval)
		if due {
			g.monitorReading = true
		}
		g.monitorMu.Unlock()
		if !due {
			continue
		}
		g.probeAccounts("", "")
		g.monitorMu.Lock()
		g.monitorReading = false
		g.monitorAt = time.Now().Unix()
		g.monitorMu.Unlock()
	}
}

// handleChannelMonitorSetting GET/POST /api/settings/channel-monitor
func (g *Gateway) handleChannelMonitorSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Minutes int `json:"minutes"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
			return
		}
		n := body.Minutes
		if n < 0 {
			n = 0
		}
		if n > 0 && n < 5 {
			n = 5
		}
		if n > 1440 {
			n = 1440
		}
		g.settingsMu.Lock()
		g.settings.MonitorMinutes = n
		err := g.saveSettings()
		g.settingsMu.Unlock()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "保存设置失败: " + err.Error()})
			return
		}
	}
	g.settingsMu.Lock()
	n := g.settings.MonitorMinutes
	g.settingsMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"minutes": n})
}

// handleChannelStatus GET /api/channels/status
func (g *Gateway) handleChannelStatus(w http.ResponseWriter, r *http.Request) {
	g.monitorMu.Lock()
	entries := make([]channelProbe, 0, len(g.monitor))
	for _, p := range g.monitor {
		cp := *p
		cp.History = append([]probeSample(nil), p.History...)
		entries = append(entries, cp)
	}
	reading := g.monitorReading
	at := g.monitorAt
	g.monitorMu.Unlock()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ProviderID != entries[j].ProviderID {
			return entries[i].ProviderID < entries[j].ProviderID
		}
		return entries[i].AccountID < entries[j].AccountID
	})
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "reading": reading, "checkedAt": at})
}

// handleChannelCheck POST /api/channels/check {"provider":"","account":""}
//
// 空 provider = 全部可用账号；异步执行，前端轮询 status。
func (g *Gateway) handleChannelCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Account  string `json:"account"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body)
	providerID := strings.TrimSpace(body.Provider)
	accountID := strings.TrimSpace(body.Account)

	g.monitorMu.Lock()
	if g.monitorReading {
		g.monitorMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"reading": true})
		return
	}
	g.monitorReading = true
	g.monitorMu.Unlock()

	go func() {
		defer func() {
			g.monitorMu.Lock()
			g.monitorReading = false
			g.monitorAt = time.Now().Unix()
			g.monitorMu.Unlock()
		}()
		g.probeAccounts(providerID, accountID)
	}()
	writeJSON(w, http.StatusOK, map[string]any{"reading": true})
}

// probeCost 是探测选模型时的成本键。
type probeCost struct {
	rate     float64 // 渠道内积分倍率；未配置按 ×1
	price    float64 // 价格表 input+output 单价之和（美元/百万 token）
	hasPrice bool
	tier     int
}

type scoredProbeModel struct {
	model ir.ModelMeta
	cost  probeCost
}

// scoreProbeModels 计算每个模型的成本键。
//
// 成本口径依次是：
//  1. 用户配置的渠道内模型积分倍率（未配置按 ×1）；
//  2. 全局模型价格表（input + output 单价之和）；
//  3. 模型名启发式：free/deepseek → flash/mini/haiku → 标准 → 未知 → pro/opus/max。
func scoreProbeModels(models []ir.ModelMeta, rates map[string]float64, priceOf func(string) (ModelPrice, bool)) ([]scoredProbeModel, bool, bool) {
	items := make([]scoredProbeModel, 0, len(models))
	anyRate, anyPrice := false, false
	for _, m := range models {
		bare := m.UpstreamModel
		if bare == "" {
			if i := strings.LastIndex(m.ID, "/"); i >= 0 {
				bare = m.ID[i+1:]
			} else {
				bare = m.ID
			}
		}
		c := probeCost{rate: 1, tier: modelCostTier(bare + " " + m.ID)}
		if v, ok := rates[bare]; ok && v > 0 {
			c.rate = v
			anyRate = true
		} else if v, ok := rates[m.ID]; ok && v > 0 {
			c.rate = v
			anyRate = true
		}
		if priceOf != nil {
			if p, ok := priceOf(bare); ok {
				c.price = p.InputPerM + p.OutputPerM
				c.hasPrice = true
				anyPrice = true
			} else if p, ok := priceOf(m.ID); ok {
				c.price = p.InputPerM + p.OutputPerM
				c.hasPrice = true
				anyPrice = true
			}
		}
		items = append(items, scoredProbeModel{model: m, cost: c})
	}
	return items, anyRate, anyPrice
}

func probeCostLess(a, b probeCost, anyRate, anyPrice bool) bool {
	if anyRate {
		if a.rate != b.rate {
			return a.rate < b.rate
		}
	} else if anyPrice {
		if a.hasPrice != b.hasPrice {
			return a.hasPrice
		}
		if a.hasPrice && a.price != b.price {
			return a.price < b.price
		}
	}
	return a.tier < b.tier
}

// orderProbeModels 按成本从低到高排序。
func orderProbeModels(models []ir.ModelMeta, rates map[string]float64, priceOf func(string) (ModelPrice, bool)) []ir.ModelMeta {
	items, anyRate, anyPrice := scoreProbeModels(models, rates, priceOf)
	sort.SliceStable(items, func(i, j int) bool {
		return probeCostLess(items[i].cost, items[j].cost, anyRate, anyPrice)
	})
	out := make([]ir.ModelMeta, 0, len(items))
	for _, it := range items {
		out = append(out, it.model)
	}
	return out
}

// probeOrder 返回按成本从低到高排序的探测候选。
func (g *Gateway) probeOrder(pid string, models []ir.ModelMeta) []ir.ModelMeta {
	return orderProbeModels(models, g.providerModelCreditMap(pid), g.priceFor)
}

// modelCostTier 是缺少倍率/价格时的成本档位：数字越小越便宜。
func modelCostTier(name string) int {
	s := strings.ToLower(name)
	switch {
	case strings.Contains(s, "deepseek") || containsToken(s, "free"):
		return 0
	// 昂贵档先判：o3-mini 这类“小号推理模型”避免被 mini 误判成最便宜。
	case containsAnyToken(s, "opus", "pro", "max", "ultra", "large", "reasoner", "thinking", "o1", "o3", "o4"):
		return 4
	case containsAnyToken(s, "flash", "lite", "mini", "nano", "small", "haiku", "turbo", "fast", "air", "instant", "light"):
		return 1
	case containsAny(s, "gpt", "gemini", "claude", "glm", "kimi", "qwen", "llama", "mistral", "command", "minimax", "hunyuan", "grok", "doubao", "hy3", "hy4", "step", "yi"):
		return 2
	default:
		return 3
	}
}

func containsAny(s string, tokens ...string) bool {
	for _, t := range tokens {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

func containsAnyToken(s string, tokens ...string) bool {
	for _, t := range tokens {
		if containsToken(s, t) {
			return true
		}
	}
	return false
}

// containsToken 按模型 ID 的分隔符（- _ / 空格 .）判断完整词，避免
// "gemini" 里的 "mini" 被误判成廉价档位。
func containsToken(s, token string) bool {
	for i := 0; i+len(token) <= len(s); i++ {
		if s[i:i+len(token)] != token {
			continue
		}
		before := i == 0 || !isModelChar(s[i-1])
		after := i+len(token) == len(s) || !isModelChar(s[i+len(token)])
		if before && after {
			return true
		}
	}
	return false
}

func isModelChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

// probeFirstOK 按清单顺序做连通性探测，最多试 attempts 个模型，返回第一个成功的。
// 失败时返回最后一次的结果，并把试过的次数写进错误文本。
func probeFirstOK(ctx context.Context, chat provider.ChatCapability, env *provider.AccountEnv,
	models []ir.ModelMeta, attempts int) (ir.ModelMeta, bool, string, int64) {
	if len(models) == 0 {
		return ir.ModelMeta{}, false, "渠道没有登记模型，无法探测", 0
	}
	if attempts < 1 || attempts > len(models) {
		attempts = len(models)
	}
	var (
		last ir.ModelMeta
		msg  string
		ms   int64
	)
	for i := 0; i < attempts; i++ {
		m := models[i]
		start := time.Now()
		c, cancel := context.WithTimeout(ctx, probeTimeout)
		ok, mmsg := probeModel(c, chat, env, m.UpstreamModel)
		cancel()
		last, msg, ms = m, mmsg, time.Since(start).Milliseconds()
		if ok {
			return last, true, "", ms
		}
	}
	if attempts > 1 {
		msg = msg + "（共试 " + fmt.Sprint(attempts) + " 个模型，均失败）"
	}
	return last, false, msg, ms
}

func (g *Gateway) probeAccounts(providerID, accountID string) {
	for _, env := range g.pool.All() {
		if providerID != "" && env.ProviderID != providerID {
			continue
		}
		if accountID != "" && env.AccountID != accountID {
			continue
		}
		if !env.Available() {
			g.recordProbe(channelProbe{
				ProviderID: env.ProviderID, AccountID: env.AccountID, Label: env.Label,
				At: time.Now().Unix(), Error: "账号当前不可用（冷却/停用/失效）",
			}, "")
			continue
		}
		p, ok := provider.Get(env.ProviderID)
		if !ok {
			g.recordProbe(channelProbe{ProviderID: env.ProviderID, AccountID: env.AccountID, Label: env.Label,
				At: time.Now().Unix(), Error: "渠道未注册"}, "")
			continue
		}
		chat, ok := provider.AsChat(p)
		if !ok {
			g.recordProbe(channelProbe{ProviderID: env.ProviderID, AccountID: env.AccountID, Label: env.Label,
				At: time.Now().Unix(), Error: "该渠道不支持对话探测"}, "")
			continue
		}
		models := p.Manifest().Models
		if len(models) == 0 {
			g.recordProbe(channelProbe{ProviderID: env.ProviderID, AccountID: env.AccountID, Label: env.Label,
				At: time.Now().Unix(), Error: "渠道没有登记模型，无法探测"}, "")
			continue
		}
		// 按成本从低到高试，最多 3 个：最便宜的失败后升级到次便宜。
		models = g.probeOrder(env.ProviderID, models)
		model, okProbe, msg, ms := probeFirstOK(context.Background(), chat, env, models, 3)
		entry := channelProbe{
			ProviderID: env.ProviderID, AccountID: env.AccountID, Label: env.Label,
			Model: model.ID, OK: okProbe, Ms: ms, At: time.Now().Unix(),
		}
		if okProbe {
			entry.Code = 200
		} else {
			entry.Error = msg
			entry.Code = statusFromText(msg)
		}
		g.recordProbe(entry, model.ID)
	}
}

func (g *Gateway) recordProbe(entry channelProbe, model string) {
	g.monitorMu.Lock()
	defer g.monitorMu.Unlock()
	if g.monitor == nil {
		g.monitor = map[string]*channelProbe{}
	}
	key := entry.ProviderID + "\x00" + entry.AccountID
	if prev, ok := g.monitor[key]; ok {
		entry.History = append(entry.History, prev.History...)
	}
	sample := probeSample{At: entry.At, OK: entry.OK, Ms: entry.Ms, Error: entry.Error}
	entry.History = append([]probeSample{sample}, entry.History...)
	if len(entry.History) > probeHistoryLimit {
		entry.History = entry.History[:probeHistoryLimit]
	}
	g.monitor[key] = &entry
}

// statusFromText 从上游错误文本里取 HTTP 状态码（"429 Too Many Requests"、
// "status 502" 等）；没有就返回 0，界面显示“失败”而不是编一个码。
func statusFromText(msg string) int {
	for _, f := range strings.FieldsFunc(msg, func(r rune) bool { return r < '0' || r > '9' }) {
		if len(f) == 3 && (f[0] == '4' || f[0] == '5') {
			n, err := strconv.Atoi(f)
			if err == nil && n >= 400 && n <= 599 {
				return n
			}
		}
	}
	return 0
}
