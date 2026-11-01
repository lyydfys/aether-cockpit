package gateway

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// GET /metrics —— Prometheus 文本格式的运行时指标。
//
// 这是"超越对标"的一项：magpie 只有 OTLP，没有给本地 Prometheus 抓取的
// 文本端点。数据全部来自已有的用量存储与账号池，抓取时聚合，不新增记账开销：
//   - aether_requests_total{provider}
//   - aether_errors_total{provider}
//   - aether_tokens_total{provider,direction}
//   - aether_cost_usd_total{provider}（按价格表 × 渠道倍率折算，与用量页同口径）
//   - aether_accounts{provider,state}
//   - aether_uptime_seconds
//   - aether_models
//
// 不输出任何提示词、密钥或会话内容。
func (g *Gateway) handleMetrics(w io.Writer) {
	type row struct {
		requests, errors, input, output, cached float64
		cost                                    float64
	}
	byProvider := map[string]*row{}
	g.usage.mu.Lock()
	for _, cells := range g.usage.days {
		for _, c := range cells {
			r := byProvider[c.ProviderID]
			if r == nil {
				r = &row{}
				byProvider[c.ProviderID] = r
			}
			r.requests += float64(c.Requests)
			r.errors += float64(c.Errors)
			r.input += float64(c.InputTokens)
			r.output += float64(c.OutputTokens)
			r.cached += float64(c.CachedTokens)
			if price, ok := g.priceFor(c.Model); ok {
				cost := costOf(price, c.InputTokens, c.OutputTokens, c.CachedTokens, c.CacheCreate)
				if rate := g.providerPriceRate(c.ProviderID); rate != 1 {
					cost *= rate
				}
				r.cost += cost
			}
		}
	}
	g.usage.mu.Unlock()

	providers := make([]string, 0, len(byProvider))
	for p := range byProvider {
		providers = append(providers, p)
	}
	sort.Strings(providers)

	var b strings.Builder
	writeMetric := func(name, help, typ string, families func()) {
		b.WriteString("# HELP " + name + " " + help + "\n")
		b.WriteString("# TYPE " + name + " " + typ + "\n")
		families()
	}
	writeMetric("aether_requests_total", "Total upstream requests by provider.", "counter", func() {
		for _, p := range providers {
			b.WriteString(fmt.Sprintf("aether_requests_total{provider=%q} %g\n", p, byProvider[p].requests))
		}
	})
	writeMetric("aether_errors_total", "Total failed upstream requests by provider.", "counter", func() {
		for _, p := range providers {
			b.WriteString(fmt.Sprintf("aether_errors_total{provider=%q} %g\n", p, byProvider[p].errors))
		}
	})
	writeMetric("aether_tokens_total", "Total input/output tokens by provider.", "counter", func() {
		for _, p := range providers {
			b.WriteString(fmt.Sprintf("aether_tokens_total{provider=%q,direction=\"input\"} %g\n", p, byProvider[p].input))
			b.WriteString(fmt.Sprintf("aether_tokens_total{provider=%q,direction=\"output\"} %g\n", p, byProvider[p].output))
			b.WriteString(fmt.Sprintf("aether_tokens_total{provider=%q,direction=\"cached\"} %g\n", p, byProvider[p].cached))
		}
	})
	writeMetric("aether_cost_usd_total", "Estimated cost in USD by provider.", "counter", func() {
		for _, p := range providers {
			b.WriteString(fmt.Sprintf("aether_cost_usd_total{provider=%q} %g\n", p, byProvider[p].cost))
		}
	})

	accounts := map[string]map[string]int{}
	for _, env := range g.pool.All() {
		snap := env.Snapshot()
		m := accounts[snap.ProviderID]
		if m == nil {
			m = map[string]int{}
			accounts[snap.ProviderID] = m
		}
		m[string(snap.State)]++
	}
	acctProviders := make([]string, 0, len(accounts))
	for p := range accounts {
		acctProviders = append(acctProviders, p)
	}
	sort.Strings(acctProviders)
	writeMetric("aether_accounts", "Accounts by provider and state.", "gauge", func() {
		for _, p := range acctProviders {
			states := make([]string, 0, len(accounts[p]))
			for s := range accounts[p] {
				states = append(states, s)
			}
			sort.Strings(states)
			for _, s := range states {
				b.WriteString(fmt.Sprintf("aether_accounts{provider=%q,state=%q} %d\n", p, s, accounts[p][s]))
			}
		}
	})
	writeMetric("aether_models", "Registered models.", "gauge", func() {
		b.WriteString(fmt.Sprintf("aether_models %d\n", len(g.models.KnownModels())))
	})
	writeMetric("aether_uptime_seconds", "Seconds since the gateway started.", "gauge", func() {
		b.WriteString(fmt.Sprintf("aether_uptime_seconds %g\n", time.Since(g.startedAt).Seconds()))
	})

	_, _ = io.WriteString(w, b.String())
}

// handleMetricsHTTP 是 /metrics 的 http 包装：纯文本，不做鉴权也不回显敏感信息。
func (g *Gateway) handleMetricsHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	g.handleMetrics(w)
}
