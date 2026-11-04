package gateway

import (
	"os"
	"sort"
	"strings"
	"time"

	"aether-cockpit/internal/core/sessions"
)

// 会话用量视图：把 sessions.Analyze 的结果与模型价格合成 aether 用量页要的
// 字段（sessions / stats.days / overview.top / sessions/one）。

type sessionModelView struct {
	Model      string  `json:"model"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
	Cost       float64 `json:"cost"`
	Priced     bool    `json:"priced"`
}

type sessionUsageView struct {
	Key        string             `json:"key"`
	ID         string             `json:"id"`
	Agent      string             `json:"agent"`
	Name       string             `json:"name"`
	Title      string             `json:"title"`
	Cwd        string             `json:"cwd"`
	Icon       string             `json:"icon,omitempty"`
	Start      int64              `json:"start"`
	Last       int64              `json:"last"`
	Input      int64              `json:"input"`
	Output     int64              `json:"output"`
	CacheRead  int64              `json:"cache_read"`
	CacheWrite int64              `json:"cache_write"`
	Cost       float64            `json:"cost"`
	Priced     bool               `json:"priced"`
	Unpriced   bool               `json:"unpriced"`
	Requests   int                `json:"requests"`
	Active     int64              `json:"active"`
	Messages   int                `json:"messages"`
	ToolCalls  int                `json:"tool_calls"`
	Models     []sessionModelView `json:"models"`
	Path       string             `json:"path,omitempty"`
	Resume     string             `json:"resume,omitempty"`
	// WSL 是会话所在发行版（空=本机）；插件据此显示 WSL 徽标与迁移选项。
	WSL       string `json:"wsl,omitempty"`
	Deletable bool   `json:"deletable"`
	Gateway   bool   `json:"gateway"`
}

type cachedSessionUsage struct {
	mod   int64
	size  int64
	usage sessions.Usage
}

func (g *Gateway) sessionUsageOf(s sessions.Session) sessions.Usage {
	key := s.Agent + ":" + s.ID
	var mod, size int64
	if st, err := os.Stat(s.Path); err == nil {
		mod, size = st.ModTime().UnixNano(), st.Size()
	}
	g.sessUseMu.Lock()
	if c, ok := g.sessUseCache[key]; ok && c.mod == mod && c.size == size {
		u := c.usage
		g.sessUseMu.Unlock()
		return u
	}
	g.sessUseMu.Unlock()
	u := sessions.Analyze(s)
	g.sessUseMu.Lock()
	if g.sessUseCache == nil {
		g.sessUseCache = map[string]cachedSessionUsage{}
	}
	g.sessUseCache[key] = cachedSessionUsage{mod: mod, size: size, usage: u}
	g.sessUseMu.Unlock()
	return u
}

func (g *Gateway) priceTable() map[string]ModelPrice {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	out := make(map[string]ModelPrice, len(g.settings.ModelPrices))
	for k, v := range g.settings.ModelPrices {
		out[k] = v
	}
	return out
}

func priceOf(prices map[string]ModelPrice, model string) (ModelPrice, bool) {
	bare := model
	if i := strings.LastIndex(model, "/"); i >= 0 {
		bare = model[i+1:]
	}
	if p, ok := matchPriceMap(prices, model); ok {
		return p, true
	}
	if bare != model {
		if p, ok := matchPriceMap(prices, bare); ok {
			return p, true
		}
	}
	// 内置官方价格表（含通配前缀）是会话用量的兜底；否则 Codex 的
	// gpt-6.1-sol 这类模型永远显示未计价。
	if p, ok := defaultPriceFor(bare); ok {
		return p, true
	}
	return ModelPrice{}, false
}

func (g *Gateway) sessionViewOf(s sessions.Session, prices map[string]ModelPrice, withPath bool) sessionUsageView {
	u := g.sessionUsageOf(s)
	v := sessionUsageView{
		Key: s.Agent + ":" + s.ID, ID: s.ID, Agent: s.Agent, Name: sessionAgentName(s.Agent),
		Title: s.Title, Cwd: s.Cwd, Icon: sessionAgentName(s.Agent),
		WSL:   s.WSL,
		Start: u.Start * 1000, Last: s.Last * 1000,
		Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite,
		Requests: u.Requests,
		Active:   u.ActiveSec, Messages: u.Messages, ToolCalls: u.ToolCalls,
		Models: []sessionModelView{},
	}
	for _, mu := range u.Models {
		mv := sessionModelView{Model: mu.Model, Input: mu.Input, Output: mu.Output, CacheRead: mu.CacheRead, CacheWrite: mu.CacheWrite}
		if p, ok := priceOf(prices, mu.Model); ok && p.InputPerM > 0 {
			cwRate := p.CachedWritePerM
			if cwRate == 0 {
				cwRate = p.InputPerM
			}
			mv.Cost = float64(mu.Input)*p.InputPerM/1e6 + float64(mu.Output)*p.OutputPerM/1e6 +
				float64(mu.CacheRead)*p.CachedReadPerM/1e6 + float64(mu.CacheWrite)*cwRate/1e6
			mv.Priced = true
			v.Cost += mv.Cost
		} else {
			v.Unpriced = true
		}
		v.Models = append(v.Models, mv)
	}
	sort.Slice(v.Models, func(i, j int) bool {
		return v.Models[i].Input+v.Models[i].Output > v.Models[j].Input+v.Models[j].Output
	})
	v.Priced = !v.Unpriced
	// 三个已支持客户端都给重开命令（与 manage 端点用同一套模板）。
	v.Resume = g.resumeCommand(s.Agent, s.ID)
	if withPath {
		v.Path = s.Path
	}
	return v
}

func (g *Gateway) allSessionViews() []sessionUsageView {
	all := sessions.Scan()
	prices := g.priceTable()
	out := make([]sessionUsageView, 0, len(all))
	for _, s := range all {
		out = append(out, g.sessionViewOf(s, prices, false))
	}
	return out
}

func dayKey(ms int64) string {
	return time.UnixMilli(ms).Format("2006-01-02")
}

func sessionMatches(v sessionUsageView, agent, model, cwd, key string) bool {
	if agent != "" && v.Agent != agent {
		return false
	}
	if cwd != "" && v.Cwd != cwd {
		return false
	}
	if key != "" && v.Key != key {
		return false
	}
	if model != "" {
		ok := false
		for _, m := range v.Models {
			if m.Model == model {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// stats 生成 aether 需要的日桶：from/to 与 days[]（usage 按会话最后活动日归档，
// active 按 agent+cwd 汇总）。
func (g *Gateway) sessionsStatsView(days int) map[string]any {
	if days <= 0 {
		days = 7
	}
	now := time.Now()
	start := now.AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	dateKeys := make([]string, 0, days)
	byDate := map[string]int{}
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i)
		k := d.Format("2006-01-02")
		dateKeys = append(dateKeys, k)
		byDate[k] = i
	}
	type dayBucket struct {
		usage  []map[string]any
		active []map[string]any
	}
	buckets := make([]dayBucket, days)
	keys := []string{}
	agents := map[string]string{"claude-code": "Claude Code", "codex": "Codex", "gemini-cli": "Gemini CLI"}
	views := g.allSessionViews()
	for _, v := range views {
		if v.Last < start.UnixMilli() {
			continue
		}
		idx, ok := byDate[dayKey(v.Last)]
		if !ok {
			idx = days - 1
		}
		keys = append(keys, v.Key)
		for _, m := range v.Models {
			buckets[idx].usage = append(buckets[idx].usage, map[string]any{
				"agent": v.Agent, "model": m.Model, "cwd": v.Cwd,
				"input": m.Input, "output": m.Output, "cache_read": m.CacheRead, "cache_write": m.CacheWrite,
				"cost": m.Cost, "priced": m.Priced,
			})
		}
		secs := v.Active
		if secs <= 0 {
			secs = v.Last/1000 - v.Start/1000
			if secs < 0 {
				secs = 0
			}
		}
		buckets[idx].active = append(buckets[idx].active, map[string]any{
			"agent": v.Agent, "cwd": v.Cwd, "seconds": secs,
		})
	}
	dayRows := make([]map[string]any, 0, days)
	for i, k := range dateKeys {
		if buckets[i].usage == nil {
			buckets[i].usage = []map[string]any{}
		}
		if buckets[i].active == nil {
			buckets[i].active = []map[string]any{}
		}
		dayRows = append(dayRows, map[string]any{"date": k, "usage": buckets[i].usage, "active": buckets[i].active})
	}
	return map[string]any{
		"from":     dateKeys[0],
		"to":       dateKeys[len(dateKeys)-1],
		"days":     dayRows,
		"agents":   agents,
		"keys":     keys,
		"sessions": len(keys),
		"total":    len(views),
	}
}

// sessionsOverviewView 生成用量页 Overview 的统计与 top/shape。
func (g *Gateway) sessionsOverviewView(days int, agent, model, cwd string) map[string]any {
	if days <= 0 {
		days = 7
	}
	start := time.Now().AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	views := g.allSessionViews()
	picked := make([]sessionUsageView, 0, len(views))
	for _, v := range views {
		if v.Last < start.UnixMilli() || !sessionMatches(v, agent, model, cwd, "") {
			continue
		}
		picked = append(picked, v)
	}
	dateKeys := make([]string, 0, days)
	dayIdx := map[string]int{}
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i)
		dateKeys = append(dateKeys, d.Format("2006-01-02"))
		dayIdx[d.Format("2006-01-02")] = i
	}
	daysCount := make([]int, days)
	daysMsgs := make([]int, days)
	tokens := make([]int64, 0, len(picked))
	for _, v := range picked {
		idx, ok := dayIdx[dayKey(v.Last)]
		if !ok {
			continue
		}
		daysCount[idx]++
		daysMsgs[idx] += v.Messages
		tokens = append(tokens, v.Input+v.Output)
	}
	median, p90 := int64(0), int64(0)
	if len(tokens) > 0 {
		sort.Slice(tokens, func(i, j int) bool { return tokens[i] < tokens[j] })
		median = tokens[len(tokens)/2]
		p90 = tokens[(len(tokens)*9)/10]
		if p90 == 0 {
			p90 = tokens[len(tokens)-1]
		}
	}
	topBy := func(less func(a, b sessionUsageView) int) []sessionUsageView {
		out := append([]sessionUsageView(nil), picked...)
		sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) < 0 })
		if len(out) > 8 {
			out = out[:8]
		}
		return out
	}
	asc := func(x, y int64) int {
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
		return 0
	}
	top := map[string]any{
		"tokens":   topBy(func(a, b sessionUsageView) int { return -asc(a.Input+a.Output, b.Input+b.Output) }),
		"output":   topBy(func(a, b sessionUsageView) int { return -asc(a.Output, b.Output) }),
		"messages": topBy(func(a, b sessionUsageView) int { return -asc(int64(a.Messages), int64(b.Messages)) }),
		"sessions": topBy(func(a, b sessionUsageView) int { return -asc(a.Active, b.Active) }),
		"active":   topBy(func(a, b sessionUsageView) int { return -asc(a.Active, b.Active) }),
		"cost":     topBy(func(a, b sessionUsageView) int { return 0 }),
	}
	return map[string]any{
		"count":    len(picked),
		"median":   median,
		"p90":      p90,
		"days":     daysCount,
		"messages": daysMsgs,
		"top":      top,
		"shape":    shapeOf(picked),
	}
}

func shapeOf(list []sessionUsageView) map[string]any {
	bucket := func(vals []int64, edges []int64) map[string]any {
		counts := make([]int, len(edges)-1)
		for _, v := range vals {
			for i := 0; i+1 < len(edges); i++ {
				if v >= edges[i] && v < edges[i+1] {
					counts[i]++
					break
				}
			}
		}
		return map[string]any{"edges": edges, "counts": counts, "total": len(vals)}
	}
	msgs := make([]int64, 0, len(list))
	mins := make([]int64, 0, len(list))
	tools := make([]int64, 0, len(list))
	for _, v := range list {
		msgs = append(msgs, int64(v.Messages))
		mins = append(mins, v.Active/60)
		tools = append(tools, int64(v.ToolCalls))
	}
	return map[string]any{
		"messages": bucket(msgs, []int64{0, 5, 10, 20, 40, 80, 160, 320, 1 << 40}),
		"minutes":  bucket(mins, []int64{0, 1, 5, 15, 30, 60, 120, 240, 1 << 40}),
		"autonomy": bucket(tools, []int64{0, 1, 2, 4, 8, 16, 32, 64, 1 << 40}),
	}
}

// sessionByKey 供 sessions/one 使用。
func (g *Gateway) sessionByKey(key string) (sessionUsageView, bool) {
	agent, id, ok := strings.Cut(key, ":")
	if !ok {
		return sessionUsageView{}, false
	}
	for _, s := range sessions.Scan() {
		if s.Agent != agent || s.ID != id {
			continue
		}
		return g.sessionViewOf(s, g.priceTable(), true), true
	}
	return sessionUsageView{}, false
}
