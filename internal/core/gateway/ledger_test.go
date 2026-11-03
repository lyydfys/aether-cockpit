package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedLedger(t *testing.T, g *Gateway) {
	t.Helper()
	today := time.Now()
	mk := func(day time.Time, agent, model, provider, account string, status int, in, out int64, cost float64, retried bool) {
		g.ledger.add(ledgerRecord{
			T: day.UnixMilli(), Agent: agent, Model: model, Req: model,
			ProviderID: provider, ProviderName: provider, AccountID: account, AccountName: account,
			In: in, Out: out, Cost: cost, Priced: true, Status: status, Retried: retried,
			TTFT: 120, Ms: 900, Source: "gateway",
		})
	}
	mk(today, "claude-code", "mock-native/echo-pro", "mock-native", "a1", 200, 100, 50, 0.002, false)
	mk(today, "codex", "mock-native/echo-pro", "mock-native", "a1", 200, 200, 80, 0.003, true)
	mk(today.AddDate(0, 0, -2), "claude-code", "openai-compat/deepseek-chat", "openai-compat", "a2", 502, 10, 0, 0, false)
}

func TestLedgerQueryFiltersAndTotals(t *testing.T) {
	g := &Gateway{ledger: newLedgerStore(filepath.Join(t.TempDir(), "ledger"))}
	t.Cleanup(func() { _ = g.ledger.Close() })
	seedLedger(t, g)

	req := httptest.NewRequest("GET", "/api/usage/requests?period=7d&limit=10", nil)
	rec := httptest.NewRecorder()
	g.handleLedger(rec, req)
	var v ledgerView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("parse: %v: %s", err, rec.Body.String())
	}
	if v.Total != 3 || len(v.Rows) != 3 {
		t.Fatalf("total/rows = %d/%d", v.Total, len(v.Rows))
	}
	if v.AvgMS != 900 || v.P95MS != 900 {
		t.Fatalf("latency stats = avg %d p95 %d", v.AvgMS, v.P95MS)
	}
	if v.Rows[0].T < v.Rows[1].T {
		t.Error("行应按时间倒序")
	}
	if v.Calls != 3 || v.Errors != 1 || v.Retried != 1 || v.Input != 310 || v.Output != 130 {
		t.Errorf("totals = %+v", v.ledgerTotalsJSON(t))
	}
	if len(v.Agents) != 2 || len(v.Providers) != 2 || len(v.Accounts) != 2 {
		t.Errorf("filters = agents %d providers %d accounts %d", len(v.Agents), len(v.Providers), len(v.Accounts))
	}
	if len(v.By["agent"]) != 2 || len(v.By["model"]) != 2 || len(v.By["account"]) != 2 || len(v.Series) != 2 {
		t.Errorf("by/series = %d/%d/%d/%d", len(v.By["agent"]), len(v.By["model"]), len(v.By["account"]), len(v.Series))
	}
	// 按账号排行必须带得上 token/费用，概览页据此裁剪被过滤账号。
	acc := map[string]ledgerRank{}
	for _, row := range v.By["account"] {
		acc[row.ID] = row
	}
	if a1 := acc["a1"]; a1.Calls != 2 || a1.Input != 300 || a1.Output != 130 || a1.Cost != 0.005 {
		t.Errorf("account a1 = %+v", a1)
	}

	// 失败筛选
	req = httptest.NewRequest("GET", "/api/usage/requests?period=7d&failed=1", nil)
	rec = httptest.NewRecorder()
	g.handleLedger(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Total != 1 || v.Rows[0].Status != 502 {
		t.Fatalf("failed filter: %+v", v.Rows)
	}

	// agent 筛选
	req = httptest.NewRequest("GET", "/api/usage/requests?period=7d&agent=codex", nil)
	rec = httptest.NewRecorder()
	g.handleLedger(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Total != 1 || v.Rows[0].Agent != "codex" {
		t.Fatalf("agent filter: %+v", v.Rows)
	}
}

func TestLedgerP95UsesNearestRank(t *testing.T) {
	g := &Gateway{ledger: newLedgerStore(filepath.Join(t.TempDir(), "ledger"))}
	t.Cleanup(func() { _ = g.ledger.Close() })
	now := time.Now()
	for i := int64(1); i <= 20; i++ {
		t := now.Add(time.Duration(i) * time.Second)
		g.ledger.add(ledgerRecord{
			T: t.UnixMilli(), Day: t.Format("2006-01-02"), Model: "m", Req: "m",
			Status: 200, Ms: i, Source: "gateway",
		})
	}
	request := httptest.NewRequest("GET", "/api/usage/requests?period=7d&limit=50", nil)
	recorder := httptest.NewRecorder()
	g.handleLedger(recorder, request)
	var view ledgerView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatalf("parse ledger response: %v", err)
	}
	if view.P95MS != 19 {
		t.Fatalf("p95 = %dms, want nearest-rank 19ms", view.P95MS)
	}
}

// ledgerTotalsJSON 只用于错误信息（避免打印整张表）。
func (v ledgerView) ledgerTotalsJSON(t *testing.T) map[string]any {
	return map[string]any{
		"calls": v.Calls, "errors": v.Errors, "retried": v.Retried,
		"in": v.Input, "out": v.Output, "cost": v.Cost,
	}
}

func TestLedgerHeatmapAndCSV(t *testing.T) {
	g := &Gateway{ledger: newLedgerStore(filepath.Join(t.TempDir(), "ledger"))}
	t.Cleanup(func() { _ = g.ledger.Close() })
	seedLedger(t, g)

	rec := httptest.NewRecorder()
	g.handleLedgerHeatmap(rec, httptest.NewRequest("GET", "/api/usage/heatmap", nil))
	var h struct {
		From string `json:"from"`
		To   string `json:"to"`
		Days []struct {
			Date  string `json:"date"`
			Calls int64  `json:"calls"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil || h.From == "" {
		t.Fatalf("heatmap: %v %s", err, rec.Body.String())
	}
	if len(h.Days) != 2 {
		t.Errorf("heatmap days = %d, want 2", len(h.Days))
	}

	rec = httptest.NewRecorder()
	g.handleLedgerCSV(rec, httptest.NewRequest("GET", "/api/usage/requests.csv?period=7d", nil))
	body := rec.Body.String()
	// 表头必须覆盖审计需要的维度（不再是只有 14 列）。
	for _, col := range []string{"time", "agent", "model", "provider", "account", "caller_key", "err_type", "upstream_truncated"} {
		if !strings.Contains(strings.SplitN(body, "\n", 2)[0], col) {
			t.Fatalf("CSV 头缺列 %q: %q", col, body[:80])
		}
	}
	if got := strings.Count(strings.TrimSpace(body), "\n") + 1; got != 4 {
		t.Errorf("CSV 行数 = %d, want 4（含表头）", got)
	}
	if !strings.Contains(body, "claude-code") || !strings.Contains(body, "0.002000") {
		t.Errorf("CSV 内容缺失: %s", body)
	}
}

func TestLedgerPaging(t *testing.T) {
	g := &Gateway{ledger: newLedgerStore(filepath.Join(t.TempDir(), "ledger"))}
	t.Cleanup(func() { _ = g.ledger.Close() })
	seedLedger(t, g)
	rec := httptest.NewRecorder()
	g.handleLedger(rec, httptest.NewRequest("GET", "/api/usage/requests?period=7d&limit=2&offset=2", nil))
	var v ledgerView
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Total != 3 || len(v.Rows) != 1 || v.Offset != 2 {
		t.Fatalf("paging: total=%d rows=%d offset=%d", v.Total, len(v.Rows), v.Offset)
	}
}

func TestChartByModelTopNAndOthers(t *testing.T) {
	days := []ledgerSeriesPoint{{Time: "2026-10-30"}, {Time: "2026-10-31"}}
	byModel := map[string]*ledgerRank{
		"a": {ID: "a", Calls: 6}, "b": {ID: "b", Calls: 5}, "c": {ID: "c", Calls: 4},
		"d": {ID: "d", Calls: 3}, "e": {ID: "e", Calls: 2}, "f": {ID: "f", Calls: 1},
		"g": {ID: "g", Calls: 1},
	}
	byModelDay := map[string]map[string]*ledgerChartPoint{
		"a": {"2026-10-30": {T: "2026-10-30", C: 3, M: 0.3}, "2026-10-31": {T: "2026-10-31", C: 3, M: 0.3}},
		"e": {"2026-10-30": {T: "2026-10-30", C: 2, M: 0.2}},
		"f": {"2026-10-31": {T: "2026-10-31", C: 1, M: 0.1}},
		"g": {"2026-10-30": {T: "2026-10-30", C: 1, M: 0.1}},
	}
	out := chartByModel(byModelDay, byModel, days)
	if len(out) != 6 {
		t.Fatalf("series = %d, want 6（top5 + 其他）", len(out))
	}
	if out[0].Name != "a" {
		t.Fatalf("首条应按调用量排序，得到 %q", out[0].Name)
	}
	for i, s := range out {
		if len(s.Points) != len(days) {
			t.Fatalf("%s 点数 = %d, want %d（必须与天数对齐）", s.Name, len(s.Points), len(days))
		}
		if s.Points[0].T != days[0].Time {
			t.Fatalf("%s 首点日期 = %q, want %q", s.Name, s.Points[0].T, days[0].Time)
		}
		_ = i
	}
	others := out[5]
	if others.Name != "其他" {
		t.Fatalf("第 4 条应是「其他」，得到 %q", others.Name)
	}
	if others.Points[0].C != 1 || others.Points[1].C != 1 {
		t.Fatalf("「其他」未把 f/g 合起来: %+v", others.Points)
	}
}

func TestLedgerHourlyBucket(t *testing.T) {
	g := &Gateway{ledger: newLedgerStore(filepath.Join(t.TempDir(), "ledger"))}
	t.Cleanup(func() { _ = g.ledger.Close() })
	day := time.Now().Format("2006-01-02")
	mk := func(h, m int, model string, calls int) {
		for i := 0; i < calls; i++ {
			ts := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), h, m+i, 0, 0, time.Local)
			g.ledger.add(ledgerRecord{
				T: ts.UnixMilli(), Day: day, Agent: "cli", Model: model, Req: model,
				ProviderID: "p", ProviderName: "P", AccountID: "a", AccountName: "A",
				In: 10, Out: 5, Cost: 0.001, Priced: true, Status: 200, Source: "gateway",
			})
		}
	}
	mk(9, 5, "m1", 2)
	mk(9, 40, "m1", 1)
	mk(10, 5, "m2", 3)

	req := httptest.NewRequest("GET", "/api/usage/requests?period=today&bucket=hour&limit=1", nil)
	rec := httptest.NewRecorder()
	g.handleLedger(rec, req)
	var v ledgerView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Bucket != "hour" {
		t.Fatalf("bucket = %q", v.Bucket)
	}
	if len(v.Series) != 2 {
		t.Fatalf("series = %+v", v.Series)
	}
	if v.Series[0].Label != "09:00" || v.Series[0].Calls != 3 || v.Series[1].Label != "10:00" || v.Series[1].Calls != 3 {
		t.Fatalf("hourly series = %+v", v.Series)
	}
	if !strings.HasSuffix(v.Series[0].Time, "T09") {
		t.Fatalf("hour key = %q", v.Series[0].Time)
	}
	byModel, _ := v.ChartBy.(map[string]any)
	models, _ := byModel["model"].([]any)
	if len(models) == 0 {
		t.Fatalf("chartBy.model = %+v", v.ChartBy)
	}
	first, _ := models[0].(map[string]any)
	points, _ := first["points"].([]any)
	if len(points) == 0 {
		t.Fatalf("模型点 = %+v", first)
	}
	pt, _ := points[0].(map[string]any)
	if ts, _ := pt["t"].(string); !strings.Contains(ts, "T") {
		t.Fatalf("模型点未按小时对齐: %+v", pt)
	}
}
