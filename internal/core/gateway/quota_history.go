package gateway

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/provider"
)

// 额度读数历史：用量页的曲线（7/30/45 天与按周期）靠它画。
//
// 只存"每个窗口每次读到的剩余百分比 + 窗口起止"，不存任何凭证；
// 落盘一个 JSON 文件（与 usage.json 同目录），进程重启后曲线仍在。
// 429 观测窗口也记：没有用量接口的渠道至少有"何时打满、何时重置"。

const (
	quotaHistMaxAge    = 46 * 24 * time.Hour
	quotaHistMaxPoints = 4000
	quotaHistMinGapSec = 60
)

type quotaHistPoint struct {
	At       int64   `json:"at"`
	Left     float64 `json:"left"`
	Start    int64   `json:"start,omitempty"`
	ResetsAt int64   `json:"resetsAt,omitempty"`
}

type quotaHistAccount struct {
	Provider string                      `json:"provider"`
	User     string                      `json:"user"`
	Lines    map[string][]quotaHistPoint `json:"lines"`
	// Balance 是余额型账号的读数历史（中继美元/积分类），用量页画趋势线。
	Balance []quotaBalancePoint `json:"balance,omitempty"`
}

type quotaBalancePoint struct {
	At     int64   `json:"at"`
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit,omitempty"`
}

type quotaHistStore struct {
	mu       sync.Mutex
	path     string
	accounts map[string]*quotaHistAccount
}

type quotaHistFile struct {
	Version  int                          `json:"version"`
	Accounts map[string]*quotaHistAccount `json:"accounts"`
}

func newQuotaHistory(path string) *quotaHistStore {
	s := &quotaHistStore{path: path, accounts: map[string]*quotaHistAccount{}}
	if path == "" {
		return s
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var f quotaHistFile
	if json.Unmarshal(raw, &f) == nil && f.Accounts != nil {
		s.accounts = f.Accounts
	}
	return s
}

// record 记录一次成功读数（q.Windows），没有窗口但总量大于 0 时合成 "Quota"。
func (s *quotaHistStore) record(providerID, user string, windows []provider.QuotaWindow, q *provider.QuotaStatus) {
	if s == nil {
		return
	}
	type point struct {
		name     string
		left     float64
		start    int64
		resetsAt int64
	}
	pts := make([]point, 0, len(windows)+1)
	for _, w := range windows {
		left := clampLeft(100 - w.Used)
		pts = append(pts, point{name: w.Name, left: left, start: w.StartsAt, resetsAt: w.ResetsAt})
	}
	if len(pts) == 0 && q != nil && q.Total > 0 {
		left := 0.0
		if q.Remaining > 0 {
			left = clampLeft(float64(q.Remaining) / float64(q.Total) * 100)
		}
		pts = append(pts, point{name: "Quota", left: left, resetsAt: q.ExpiresAt})
	}
	now := time.Now().Unix()
	if len(pts) == 0 && (q == nil || q.Balance <= 0) {
		return
	}
	key := providerID + "\x00" + strings.ToLower(strings.TrimSpace(user))
	s.mu.Lock()
	acct := s.accounts[key]
	if acct == nil {
		acct = &quotaHistAccount{Provider: providerID, User: strings.ToLower(strings.TrimSpace(user)), Lines: map[string][]quotaHistPoint{}}
		s.accounts[key] = acct
	}
	if acct.Lines == nil {
		acct.Lines = map[string][]quotaHistPoint{}
	}
	changed := false
	for _, p := range pts {
		if p.name == "" {
			continue
		}
		list := acct.Lines[p.name]
		if n := len(list); n > 0 {
			last := list[n-1]
			// 一分钟内完全相同（含重置时间）的重复读数不重复描点。
			if last.Left == p.left && last.ResetsAt == p.resetsAt && now-last.At < quotaHistMinGapSec {
				continue
			}
		}
		list = append(list, quotaHistPoint{At: now, Left: p.left, Start: p.start, ResetsAt: p.resetsAt})
		// 保留 46 天、最多 4000 点。
		cut := now - int64(quotaHistMaxAge.Seconds())
		start := 0
		for start < len(list) && list[start].At < cut {
			start++
		}
		if start > 0 {
			list = list[start:]
		}
		if len(list) > quotaHistMaxPoints {
			list = list[len(list)-quotaHistMaxPoints:]
		}
		acct.Lines[p.name] = list
		changed = true
	}
	if q != nil && q.Balance > 0 {
		if s.appendBalanceLocked(acct, now, q.Balance, q.BalanceUnit) {
			changed = true
		}
	}
	if changed {
		s.saveLocked()
	}
	s.mu.Unlock()
}

// appendBalanceLocked 记录一个余额点（调用方持有锁）。
func (s *quotaHistStore) appendBalanceLocked(acct *quotaHistAccount, at int64, amount float64, unit string) bool {
	if n := len(acct.Balance); n > 0 {
		last := acct.Balance[n-1]
		if last.Amount == amount && last.Unit == unit && at-last.At < quotaHistMinGapSec {
			return false
		}
	}
	acct.Balance = append(acct.Balance, quotaBalancePoint{At: at, Amount: amount, Unit: unit})
	cut := at - int64(quotaHistMaxAge.Seconds())
	start := 0
	for start < len(acct.Balance) && acct.Balance[start].At < cut {
		start++
	}
	if start > 0 {
		acct.Balance = acct.Balance[start:]
	}
	if len(acct.Balance) > quotaHistMaxPoints {
		acct.Balance = acct.Balance[len(acct.Balance)-quotaHistMaxPoints:]
	}
	return true
}

// recordBalanceAt 是测试与补录用入口：在指定时间记一个余额点。
func (s *quotaHistStore) recordBalanceAt(providerID, user string, at int64, amount float64, unit string) {
	if s == nil || amount <= 0 {
		return
	}
	key := providerID + "\x00" + strings.ToLower(strings.TrimSpace(user))
	s.mu.Lock()
	acct := s.accounts[key]
	if acct == nil {
		acct = &quotaHistAccount{Provider: providerID, User: strings.ToLower(strings.TrimSpace(user)), Lines: map[string][]quotaHistPoint{}}
		s.accounts[key] = acct
	}
	if s.appendBalanceLocked(acct, at, amount, unit) {
		s.saveLocked()
	}
	s.mu.Unlock()
}

// balanceTrend 计算某个账号的余额走势（magpie 的 balanceTrend）：
// points（ISO 时间 + 金额）、currency，以及最近一段的日均消耗与耗尽时间。
// 少于 2 个点返回 nil（前端也要求至少 2 点才画线）。
func (s *quotaHistStore) balanceTrend(providerID, user string) map[string]any {
	if s == nil {
		return nil
	}
	key := providerID + "\x00" + strings.ToLower(strings.TrimSpace(user))
	s.mu.Lock()
	acct := s.accounts[key]
	var pts []quotaBalancePoint
	if acct != nil {
		pts = append(pts, acct.Balance...)
	}
	s.mu.Unlock()
	if len(pts) < 2 {
		return nil
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].At < pts[j].At })

	// 最近一次充值（余额上升）或单位变化之后才拟合消耗速度：
	// 美元与积分不能放在同一条趋势线上。
	unit := pts[len(pts)-1].Unit
	fitStart := pts[0].At
	for i := 1; i < len(pts); i++ {
		unitChanged := pts[i].Unit != pts[i-1].Unit
		toppedUp := pts[i].Amount > pts[i-1].Amount+pts[i-1].Amount*0.02
		if unitChanged || toppedUp {
			fitStart = pts[i].At
		}
	}
	var fit []quotaBalancePoint
	for _, p := range pts {
		if p.At >= fitStart && p.Unit == unit {
			fit = append(fit, p)
		}
	}
	if len(fit) < 2 {
		fit = pts[len(pts)-2:]
		fitStart = fit[0].At
	}
	first, last := fit[0], fit[len(fit)-1]
	perDay := 0.0
	if len(fit) >= 2 {
		// 最小二乘拟合「余额随时间」的斜率，再换算成每天消耗。
		var sumX, sumY, sumXY, sumXX float64
		n := float64(len(fit))
		for _, p := range fit {
			x := float64(p.At)
			y := p.Amount
			sumX += x
			sumY += y
			sumXY += x * y
			sumXX += x * x
		}
		den := n*sumXX - sumX*sumX
		if den != 0 {
			slope := (n*sumXY - sumX*sumY) / den
			if slope < 0 {
				perDay = -slope * 86400
			}
		}
	}
	out := map[string]any{
		"points":   make([]map[string]any, 0, len(fit)),
		"fitStart": time.Unix(first.At, 0).Format(time.RFC3339),
		"perDay":   perDay,
	}
	currency := ""
	if last.Unit == "USD" {
		currency = "$"
	}
	if currency != "" {
		out["currency"] = currency
	}
	if perDay > 0 {
		hoursLeft := last.Amount / perDay * 24
		if hoursLeft > 0 && hoursLeft < 24*60 {
			out["runsOut"] = time.Unix(last.At+int64(hoursLeft*3600), 0).Format(time.RFC3339)
		}
	}
	for _, p := range fit {
		out["points"] = append(out["points"].([]map[string]any), map[string]any{
			"at": time.Unix(p.At, 0).Format(time.RFC3339), "amount": p.Amount,
		})
	}
	return out
}

func clampLeft(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// saveLocked 原子写盘（调用方持有锁）。
func (s *quotaHistStore) saveLocked() {
	if s.path == "" {
		return
	}
	b, err := json.Marshal(quotaHistFile{Version: 1, Accounts: s.accounts})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		log.Printf("[WARN] 额度历史目录创建失败: %v", err)
		return
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Printf("[WARN] 额度历史写入失败: %v", err)
		return
	}
	if err := os.Rename(tmp, s.path); err != nil {
		log.Printf("[WARN] 额度历史落盘失败: %v", err)
	}
}

// view 返回 /api/usage/quotas/history 的响应：每个账号的窗口与读数点。
func (s *quotaHistStore) view(days int) []map[string]any {
	if s == nil {
		return []map[string]any{}
	}
	if days <= 0 {
		days = 45
	}
	cut := time.Now().AddDate(0, 0, -days).Unix()
	s.mu.Lock()
	keys := make([]string, 0, len(s.accounts))
	for k := range s.accounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		acct := s.accounts[k]
		if acct == nil {
			continue
		}
		names := make([]string, 0, len(acct.Lines))
		for name := range acct.Lines {
			names = append(names, name)
		}
		sort.Strings(names)
		lines := make([]map[string]any, 0, len(names))
		for _, name := range names {
			pts := make([]map[string]any, 0, len(acct.Lines[name]))
			for _, p := range acct.Lines[name] {
				if p.At < cut {
					continue
				}
				row := map[string]any{
					"at":   time.Unix(p.At, 0).UTC().Format(time.RFC3339),
					"left": p.Left,
				}
				if p.Start > 0 {
					row["start"] = time.Unix(p.Start, 0).UTC().Format(time.RFC3339)
				}
				if p.ResetsAt > 0 {
					row["resetsAt"] = time.Unix(p.ResetsAt, 0).UTC().Format(time.RFC3339)
				}
				pts = append(pts, row)
			}
			if len(pts) > 0 {
				lines = append(lines, map[string]any{"name": name, "points": pts})
			}
		}
		if len(lines) == 0 {
			continue
		}
		out = append(out, map[string]any{
			"provider": acct.Provider,
			"user":     acct.User,
			"lines":    lines,
		})
	}
	s.mu.Unlock()
	return out
}

// quotaHistoryHolds 估算某个已完成周期里"整个窗口有多大"：
// 期间经过本网关的 token / 成本 ÷ 厂商给出的已用百分比。
// 只在数据诚实可估时给 holds；否则给 holdsWhy（low/none/clock）。
func (g *Gateway) quotaHistoryHolds(provider, user string, from, to time.Time) []map[string]any {
	if g.quotaHist == nil {
		return []map[string]any{}
	}
	userKey := strings.ToLower(strings.TrimSpace(user))
	g.quotaHist.mu.Lock()
	type lineCopy struct {
		name string
		pts  []quotaHistPoint
	}
	var lines []lineCopy
	for _, acct := range g.quotaHist.accounts {
		if acct.Provider != provider || acct.User != userKey {
			continue
		}
		names := make([]string, 0, len(acct.Lines))
		for name := range acct.Lines {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			pts := append([]quotaHistPoint(nil), acct.Lines[name]...)
			lines = append(lines, lineCopy{name: name, pts: pts})
		}
	}
	g.quotaHist.mu.Unlock()
	if len(lines) == 0 {
		return []map[string]any{}
	}

	var calls, tokens, cacheRead int64
	var callTimes []int64
	var cost float64
	pricedAll := true
	if g.ledger != nil {
		recs := g.ledger.records(from.Format("2006-01-02"), to.Format("2006-01-02"))
		fromMs, toMs := from.UnixMilli(), to.UnixMilli()
		for _, rec := range recs {
			if rec.ProviderID != provider || rec.T < fromMs || rec.T > toMs {
				continue
			}
			label := rec.AccountName
			if label == "" {
				label = rec.AccountID
			}
			if strings.ToLower(strings.TrimSpace(label)) != userKey {
				continue
			}
			calls++
			callTimes = append(callTimes, rec.T)
			tokens += rec.In + rec.Out
			cacheRead += rec.CacheRead
			cost += rec.Cost
			if !rec.Priced {
				pricedAll = false
			}
		}
	}

	windows := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		var last *quotaHistPoint
		for i := range l.pts {
			p := &l.pts[i]
			if p.At >= from.Unix() && p.At <= to.Unix() {
				last = p // 取周期内最后一个读数
			}
		}
		if last == nil {
			continue
		}
		used := 100 - last.Left
		win := map[string]any{"name": l.name}
		// 干涸区间：读到 0 之后、回满/重置之前仍有调用，那些调用可能由
		// credits / extra usage 支付，不能按窗口百分比放大。
		credits := false
		for i := range l.pts {
			p := l.pts[i]
			if p.Left > 0 || p.At < from.Unix() || p.At > to.Unix() {
				continue
			}
			end := to.Unix()
			if i+1 < len(l.pts) && l.pts[i+1].Left > 0 && l.pts[i+1].At < end {
				end = l.pts[i+1].At
			}
			if p.ResetsAt > 0 && p.ResetsAt > p.At && p.ResetsAt < end {
				end = p.ResetsAt
			}
			for _, ct := range callTimes {
				if ct >= p.At*1000 && ct < end*1000 {
					credits = true
					break
				}
			}
			if credits {
				break
			}
		}
		earliest := ""
		if g.ledger != nil {
			earliest = g.ledger.earliestDay()
		}
		logMissing := false
		if from.Before(time.Now().AddDate(0, 0, -30)) {
			logMissing = true
		}
		if earliest != "" && from.Format("2006-01-02") < earliest {
			logMissing = true
		}
		switch {
		case used < 5:
			win["holdsWhy"] = "low"
		case credits:
			win["holdsWhy"] = "credits"
		case logMissing:
			win["holdsWhy"] = "log"
		case calls == 0:
			win["holdsWhy"] = "none"
		case last.Start == 0 && last.ResetsAt == 0:
			win["holdsWhy"] = "clock"
		default:
			scale := used / 100
			since := from
			if last.Start > 0 {
				since = time.Unix(last.Start, 0)
			}
			win["holds"] = map[string]any{
				"tokens": int64(float64(tokens)/scale + 0.5),
				"cost":   cost / scale,
				"priced": pricedAll && calls > 0,
				"used":   used,
				"routed": map[string]any{"tokens": tokens, "calls": calls, "cacheRead": cacheRead},
				"since":  since.UTC().Format(time.RFC3339),
			}
		}
		windows = append(windows, win)
	}
	if len(windows) == 0 {
		return []map[string]any{}
	}
	return []map[string]any{{"provider": provider, "user": user, "windows": windows}}
}
