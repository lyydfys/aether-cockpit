package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/provider"
)

func TestQuotaHistoryRecordAndView(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-history.json")
	s := newQuotaHistory(path)
	now := time.Now().Unix()
	s.record("claude", "Alice", []provider.QuotaWindow{
		{Name: "5h", Used: 40, ResetsAt: now + 3600},
		{Name: "7d", Used: 10},
	}, nil)

	out := s.view(45)
	if len(out) != 1 {
		t.Fatalf("view = %#v", out)
	}
	if out[0]["provider"] != "claude" || out[0]["user"] != "alice" {
		t.Fatalf("account = %#v", out[0])
	}
	lines := out[0]["lines"].([]map[string]any)
	if len(lines) != 2 {
		t.Fatalf("lines = %#v", lines)
	}
	pts := lines[0]["points"].([]map[string]any)
	if lines[0]["name"] != "5h" || pts[0]["left"].(float64) != 60 {
		t.Fatalf("5h line = %#v", lines[0])
	}
	if _, err := time.Parse(time.RFC3339, pts[0]["at"].(string)); err != nil {
		t.Fatalf("at 不是 RFC3339: %v", pts[0]["at"])
	}
	if _, ok := pts[0]["resetsAt"]; !ok {
		t.Fatalf("缺少 resetsAt: %#v", pts[0])
	}

	// 一分钟内完全相同的读数不重复描点。
	s.record("claude", "Alice", []provider.QuotaWindow{{Name: "5h", Used: 40, ResetsAt: now + 3600}}, nil)
	if got := len(s.view(45)[0]["lines"].([]map[string]any)[0]["points"].([]map[string]any)); got != 1 {
		t.Fatalf("重复读数被记了 %d 次", got)
	}
	// 读数变化则新增一个点。
	s.record("claude", "Alice", []provider.QuotaWindow{{Name: "5h", Used: 55, ResetsAt: now + 3600}}, nil)
	if got := len(s.view(45)[0]["lines"].([]map[string]any)[0]["points"].([]map[string]any)); got != 2 {
		t.Fatalf("变化后点数 = %d", got)
	}

	// 重开仍然存在。
	s2 := newQuotaHistory(path)
	if len(s2.view(45)) != 1 {
		t.Fatal("额度历史未持久化")
	}
}

func TestQuotaHistorySyntheticQuota(t *testing.T) {
	s := newQuotaHistory("")
	s.record("mock-native", "demo", nil, &provider.QuotaStatus{Total: 100, Remaining: 25, ExpiresAt: time.Now().Add(time.Hour).Unix()})
	lines := s.view(45)[0]["lines"].([]map[string]any)
	if len(lines) != 1 || lines[0]["name"] != "Quota" {
		t.Fatalf("lines = %#v", lines)
	}
	if pts := lines[0]["points"].([]map[string]any); pts[0]["left"].(float64) != 25 {
		t.Fatalf("left = %#v", pts[0])
	}
}

func TestQuotaHistoryDaysFilter(t *testing.T) {
	s := newQuotaHistory("")
	s.record("p", "u", []provider.QuotaWindow{{Name: "5h", Used: 10}}, nil)
	s.mu.Lock()
	s.accounts["p\x00u"].Lines["5h"] = append([]quotaHistPoint{{At: time.Now().AddDate(0, 0, -40).Unix(), Left: 90}}, s.accounts["p\x00u"].Lines["5h"]...)
	s.mu.Unlock()
	if got := len(s.view(7)[0]["lines"].([]map[string]any)[0]["points"].([]map[string]any)); got != 1 {
		t.Fatalf("7 天窗口应排除 40 天前的点，得到 %d", got)
	}
	if got := len(s.view(45)[0]["lines"].([]map[string]any)[0]["points"].([]map[string]any)); got != 2 {
		t.Fatalf("45 天窗口应包含两个点，得到 %d", got)
	}
}

func TestQuotaHistoryHandler(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: filepath.Join(t.TempDir(), "usage.json")})
	if g.quotaHist == nil {
		t.Fatal("quotaHist 未初始化")
	}
	g.quotaHist.record("mock-native", "Demo", []provider.QuotaWindow{{Name: "5h", Used: 20}}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/usage/quotas/history?days=45", nil)
	rec := httptest.NewRecorder()
	g.handleUsageQuotasHistory(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0]["provider"] != "mock-native" {
		t.Fatalf("out = %#v", out)
	}
	// holds 仍是空数组（估算未实现）。
	rec = httptest.NewRecorder()
	g.handleQuotaHistoryHolds(rec, httptest.NewRequest(http.MethodGet, "/api/usage/quotas/history/holds", nil))
	if rec.Body.String() != "[]\n" {
		t.Fatalf("holds = %q", rec.Body.String())
	}
}

func holdsWindowNow() (time.Time, time.Time, time.Time) {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from := now.Add(-2 * time.Hour)
	if from.Before(dayStart) {
		from = dayStart
	}
	return now, from, now.Add(time.Hour)
}

func TestQuotaHistoryHoldsEstimate(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: filepath.Join(dir, "usage.json")})
	_, from, to := holdsWindowNow()
	g.quotaHist.record("claude", "Alice", []provider.QuotaWindow{{
		Name: "5h", Used: 50, StartsAt: from.Unix(), ResetsAt: to.Unix(),
	}}, nil)
	g.ledger.add(ledgerRecord{ProviderID: "claude", AccountName: "Alice", T: from.Add(5 * time.Minute).UnixMilli(), In: 100, Out: 50, CacheRead: 10, Cost: 0.01, Priced: true})
	g.ledger.add(ledgerRecord{ProviderID: "claude", AccountName: "Alice", T: from.Add(10 * time.Minute).UnixMilli(), In: 100, Out: 50, CacheRead: 10, Cost: 0.01, Priced: true})
	t.Cleanup(func() { _ = g.ledger.Close() })

	out := g.quotaHistoryHolds("claude", "alice", from, to)
	if len(out) != 1 {
		t.Fatalf("out = %#v", out)
	}
	wins := out[0]["windows"].([]map[string]any)
	if len(wins) != 1 || wins[0]["name"] != "5h" {
		t.Fatalf("windows = %#v", wins)
	}
	h, _ := wins[0]["holds"].(map[string]any)
	if h == nil {
		t.Fatalf("holds = %#v", wins[0])
	}
	if h["tokens"].(int64) != 600 || h["used"].(float64) != 50 {
		t.Fatalf("holds = %#v", h)
	}
	if h["priced"] != true || h["cost"].(float64) < 0.039 || h["cost"].(float64) > 0.041 {
		t.Fatalf("cost/priced = %#v", h)
	}
	routed := h["routed"].(map[string]any)
	if routed["calls"].(int64) != 2 || routed["cacheRead"].(int64) != 20 {
		t.Fatalf("routed = %#v", routed)
	}
}

func TestQuotaHistoryHoldsReasons(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: filepath.Join(dir, "usage.json")})
	_, from, to := holdsWindowNow()

	// 用量太小：low。
	g.quotaHist.record("p", "u", []provider.QuotaWindow{{Name: "5h", Used: 2, StartsAt: from.Unix(), ResetsAt: to.Unix()}}, nil)
	out := g.quotaHistoryHolds("p", "u", from, to)
	if out[0]["windows"].([]map[string]any)[0]["holdsWhy"] != "low" {
		t.Fatalf("low: %#v", out)
	}

	// 没有调用：none。
	g.quotaHist.record("p2", "u", []provider.QuotaWindow{{Name: "5h", Used: 50, StartsAt: from.Unix(), ResetsAt: to.Unix()}}, nil)
	out = g.quotaHistoryHolds("p2", "u", from, to)
	if out[0]["windows"].([]map[string]any)[0]["holdsWhy"] != "none" {
		t.Fatalf("none: %#v", out)
	}

	// 没有窗口起止：clock（有调用）。
	g.quotaHist.record("p3", "u", []provider.QuotaWindow{{Name: "5h", Used: 50}}, nil)
	g.ledger.add(ledgerRecord{ProviderID: "p3", AccountName: "u", T: from.Add(5 * time.Minute).UnixMilli(), In: 10, Out: 5, Cost: 0.001, Priced: true})
	t.Cleanup(func() { _ = g.ledger.Close() })
	out = g.quotaHistoryHolds("p3", "u", from, to)
	if out[0]["windows"].([]map[string]any)[0]["holdsWhy"] != "clock" {
		t.Fatalf("clock: %#v", out)
	}
}

func TestQuotaHistoryHoldsCreditsReason(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: filepath.Join(dir, "usage.json")})
	now := time.Now()
	from := now.Add(-2 * time.Hour)
	to := now.Add(time.Hour)
	g.quotaHist.accounts["p\x00u"] = &quotaHistAccount{
		Provider: "p", User: "u",
		Lines: map[string][]quotaHistPoint{"5h": {
			{At: from.Add(5 * time.Minute).Unix(), Left: 0, Start: from.Unix(), ResetsAt: to.Unix()},
			{At: now.Add(-10 * time.Minute).Unix(), Left: 50, Start: from.Unix(), ResetsAt: to.Unix()},
		}},
	}
	// 干涸区间里仍有调用：可能由 credits 支付，不能按百分比放大。
	g.ledger.add(ledgerRecord{ProviderID: "p", AccountName: "u", T: from.Add(10 * time.Minute).UnixMilli(), In: 10, Out: 5, Priced: true})
	t.Cleanup(func() { _ = g.ledger.Close() })

	out := g.quotaHistoryHolds("p", "u", from, to)
	if got := out[0]["windows"].([]map[string]any)[0]["holdsWhy"]; got != "credits" {
		t.Fatalf("holdsWhy = %#v", got)
	}
}

func TestQuotaHistoryHoldsLogReason(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: filepath.Join(dir, "usage.json")})
	now := time.Now()
	from := now.AddDate(0, 0, -40)
	to := now.AddDate(0, 0, -35)
	g.quotaHist.accounts["p\x00u"] = &quotaHistAccount{
		Provider: "p", User: "u",
		Lines: map[string][]quotaHistPoint{"7d": {
			{At: now.AddDate(0, 0, -38).Unix(), Left: 50, Start: from.Unix(), ResetsAt: to.Unix()},
		}},
	}
	out := g.quotaHistoryHolds("p", "u", from, to)
	if got := out[0]["windows"].([]map[string]any)[0]["holdsWhy"]; got != "log" {
		t.Fatalf("holdsWhy = %#v", got)
	}
}

func TestBalanceTrend(t *testing.T) {
	dir := t.TempDir()
	s := newQuotaHistory(filepath.Join(dir, "quota-history.json"))
	now := time.Now().Unix()
	s.recordBalanceAt("codex", "Relay", now-7200, 10, "USD")
	s.recordBalanceAt("codex", "Relay", now-3600, 9, "USD")
	s.recordBalanceAt("codex", "Relay", now, 8, "USD")

	tr := s.balanceTrend("codex", "relay")
	if tr == nil {
		t.Fatal("trend 为空")
	}
	if tr["currency"] != "$" {
		t.Fatalf("currency = %v", tr["currency"])
	}
	pts := tr["points"].([]map[string]any)
	if len(pts) != 3 || pts[2]["amount"].(float64) != 8 {
		t.Fatalf("points = %#v", pts)
	}
	perDay, _ := tr["perDay"].(float64)
	if perDay < 23.9 || perDay > 24.1 {
		t.Fatalf("perDay = %v", perDay)
	}
	if tr["runsOut"] == nil {
		t.Fatalf("runsOut 缺失: %#v", tr)
	}
	if s.balanceTrend("codex", "nobody") != nil {
		t.Fatal("无历史不应有 trend")
	}
}

func TestBalanceTrendOnCard(t *testing.T) {
	g := &Gateway{pool: pool.New(), settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.quotaHist = newQuotaHistory(filepath.Join(t.TempDir(), "q.json"))
	now := time.Now().Unix()
	g.quotaHist.recordBalanceAt("openai-compat", "中继", now-3600, 10, "USD")
	g.quotaHist.recordBalanceAt("openai-compat", "中继", now, 8, "USD")

	env := provider.NewAccountEnv("acc-trend", "openai-compat")
	env.Label = "中继"
	env.SetQuota(&provider.QuotaStatus{Balance: 8, BalanceUnit: "USD", ReadAt: now})
	g.pool.Add(env)

	cards, _ := g.usageQuotaCards()
	if len(cards) != 1 {
		t.Fatalf("cards = %d", len(cards))
	}
	if cards[0].Balance != "$8" || cards[0].BalanceTrend == nil {
		t.Fatalf("card = %+v", cards[0])
	}
	if got := cards[0].BalanceTrend["points"].([]map[string]any); len(got) != 2 {
		t.Fatalf("points = %#v", got)
	}
}

func TestBalanceTrendLeastSquares(t *testing.T) {
	dir := t.TempDir()
	s := newQuotaHistory(filepath.Join(dir, "q.json"))
	now := time.Now().Unix()
	// 带噪声的下降序列；最小二乘斜率应接近 24/天。
	pts := []struct {
		ago  int64
		left float64
	}{{7200, 10}, {5400, 9.7}, {3600, 9.3}, {1800, 8.7}, {0, 8.0}}
	for _, p := range pts {
		s.recordBalanceAt("p", "u", now-p.ago, p.left, "USD")
	}
	tr := s.balanceTrend("p", "u")
	if tr == nil {
		t.Fatal("trend 为空")
	}
	perDay, _ := tr["perDay"].(float64)
	if perDay < 22 || perDay > 26 {
		t.Fatalf("perDay = %v, want ≈24", perDay)
	}
}

func TestBalanceTrendSeparatesUnits(t *testing.T) {
	dir := t.TempDir()
	s := newQuotaHistory(filepath.Join(dir, "q.json"))
	now := time.Now().Unix()
	// 先美元，后积分：趋势只能用最后一段（积分），不能把两种单位混在一起。
	s.recordBalanceAt("p", "u", now-7200, 10, "USD")
	s.recordBalanceAt("p", "u", now-5400, 8, "USD")
	s.recordBalanceAt("p", "u", now-3600, 100, "credits")
	s.recordBalanceAt("p", "u", now-1800, 95, "credits")
	s.recordBalanceAt("p", "u", now, 90, "credits")
	tr := s.balanceTrend("p", "u")
	if tr == nil {
		t.Fatal("trend 为空")
	}
	pts := tr["points"].([]map[string]any)
	if len(pts) != 3 {
		t.Fatalf("points = %#v（应只含积分段）", pts)
	}
	if tr["currency"] != nil {
		t.Fatalf("currency = %v（积分不应带 $）", tr["currency"])
	}
	perDay, _ := tr["perDay"].(float64)
	if perDay < 230 || perDay > 250 {
		t.Fatalf("perDay = %v", perDay)
	}
}
