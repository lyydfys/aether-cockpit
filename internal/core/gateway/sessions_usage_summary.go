package gateway

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/sessions"
)

// 会话用量汇总：只看各 Agent 自己写在本机的会话 JSONL，不依赖官方额度
// 或网关请求账本。Codex 的 token_count 是累计值，sessions.Analyze 已按
// 差值拆到模型与日期；这里只做时间窗过滤、定价与分桶。
//
// GET /api/sessions/usage?days=7&agent=codex
type sessionUsageTotals struct {
	Input      int64   `json:"input"`
	CacheRead  int64   `json:"cache_read"`
	CacheWrite int64   `json:"cache_write"`
	Output     int64   `json:"output"`
	Tokens     int64   `json:"tokens"`
	Requests   int64   `json:"requests"`
	Cost       float64 `json:"cost"`
}

type sessionUsageDayRow struct {
	Date string `json:"date"`
	sessionUsageTotals
}

type sessionUsageModelRow struct {
	Name string `json:"name"`
	sessionUsageTotals
}

type sessionUsageAgentRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Sessions int    `json:"sessions"`
	sessionUsageTotals
}

func (t *sessionUsageTotals) add(in, cr, cw, out, requests int64, cost float64) {
	t.Input += in
	t.CacheRead += cr
	t.CacheWrite += cw
	t.Output += out
	t.Tokens += in + cr + cw + out
	t.Requests += requests
	t.Cost += cost
}

// sessionUsageCost 用基础价估算（会话合计跨越多档位，逐请求档位在会话
// 日志里拿不到；这里宁可给出保守的基准价，也不假装精确）。
func sessionUsageCost(p ModelPrice, in, out, cr, cw int64) float64 {
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
		float64(cr)/1e6*readRate +
		float64(cw)/1e6*writeRate
}

func (g *Gateway) handleSessionsUsageSummary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days := 7
	switch {
	case q.Get("period") == "today":
		days = 1
	case q.Get("period") == "7d":
		days = 7
	case q.Get("period") == "30d":
		days = 30
	case q.Get("period") == "all":
		days = 90
	}
	if v := q.Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 90 {
			days = n
		}
	}
	agentFilter := strings.TrimSpace(q.Get("agent"))
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))
	to := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, now.Location())
	fromKey, toKey := from.Format("2006-01-02"), to.Format("2006-01-02")

	all := sessions.Scan()
	// 每个请求只解析一次价格（官方目录 + 用户自定义）。
	type priceResult struct {
		p  ModelPrice
		ok bool
	}
	priceCache := map[string]priceResult{}
	priceFor := func(model string) (ModelPrice, bool) {
		if v, ok := priceCache[model]; ok {
			return v.p, v.ok
		}
		p, ok := g.priceFor(model)
		priceCache[model] = priceResult{p: p, ok: ok}
		return p, ok
	}

	total := sessionUsageTotals{}
	byDay := map[string]*sessionUsageDayRow{}
	byModel := map[string]*sessionUsageModelRow{}
	byAgent := map[string]*sessionUsageAgentRow{}
	inRangeSessions := 0
	unpriced := int64(0)

	for _, s := range all {
		if agentFilter != "" && s.Agent != agentFilter {
			continue
		}
		if s.Last < from.Unix() {
			continue
		}
		u := g.sessionUsageOf(s)
		rows := 0
		for day, du := range u.Days {
			if day < fromKey || day > toKey {
				continue
			}
			rows++
			dayCost := 0.0
			for model, mu := range du.Models {
				p, ok := priceFor(model)
				cost := 0.0
				if ok {
					cost = sessionUsageCost(p, mu.Input, mu.Output, mu.CacheRead, mu.CacheWrite)
				} else {
					unpriced += int64(mu.Requests)
				}
				dayCost += cost
				mr := byModel[model]
				if mr == nil {
					mr = &sessionUsageModelRow{Name: model}
					byModel[model] = mr
				}
				mr.add(mu.Input, mu.CacheRead, mu.CacheWrite, mu.Output, int64(mu.Requests), cost)
			}
			dr := byDay[day]
			if dr == nil {
				dr = &sessionUsageDayRow{Date: day}
				byDay[day] = dr
			}
			dr.add(du.Input, du.CacheRead, du.CacheWrite, du.Output, int64(du.Requests), dayCost)
			total.add(du.Input, du.CacheRead, du.CacheWrite, du.Output, int64(du.Requests), dayCost)
		}
		if rows == 0 {
			continue
		}
		inRangeSessions++
		ar := byAgent[s.Agent]
		if ar == nil {
			ar = &sessionUsageAgentRow{ID: s.Agent, Name: sessionAgentName(s.Agent)}
			byAgent[s.Agent] = ar
		}
		ar.Sessions++
		// 用当天行加上该 Agent，避免再遍历一次原始用量。
		for day := range u.Days {
			if day < fromKey || day > toKey {
				continue
			}
			du := u.Days[day]
			cost := 0.0
			for model, mu := range du.Models {
				if p, ok := priceFor(model); ok {
					cost += sessionUsageCost(p, mu.Input, mu.Output, mu.CacheRead, mu.CacheWrite)
				}
			}
			ar.add(du.Input, du.CacheRead, du.CacheWrite, du.Output, int64(du.Requests), cost)
		}
	}

	series := make([]sessionUsageDayRow, 0, days)
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		if row := byDay[k]; row != nil {
			series = append(series, *row)
		} else {
			series = append(series, sessionUsageDayRow{Date: k})
		}
	}

	modelRows := make([]sessionUsageModelRow, 0, len(byModel))
	for _, row := range byModel {
		modelRows = append(modelRows, *row)
	}
	sort.Slice(modelRows, func(i, j int) bool {
		if modelRows[i].Tokens != modelRows[j].Tokens {
			return modelRows[i].Tokens > modelRows[j].Tokens
		}
		return modelRows[i].Name < modelRows[j].Name
	})
	agentRows := make([]sessionUsageAgentRow, 0, len(byAgent))
	for _, row := range byAgent {
		agentRows = append(agentRows, *row)
	}
	sort.Slice(agentRows, func(i, j int) bool {
		if agentRows[i].Tokens != agentRows[j].Tokens {
			return agentRows[i].Tokens > agentRows[j].Tokens
		}
		return agentRows[i].Name < agentRows[j].Name
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"from": fromKey, "to": toKey, "days": days, "agent": agentFilter,
		"files": len(all), "sessions": inRangeSessions,
		"input": total.Input, "cache_read": total.CacheRead, "cache_write": total.CacheWrite,
		"output": total.Output, "tokens": total.Tokens, "requests": total.Requests,
		"cost": total.Cost, "unpriced": unpriced,
		"series": series, "models": modelRows, "agents": agentRows,
		"last_sync": now.Unix(),
	})
}
