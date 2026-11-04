package claude

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

// 新版权限形状：除了顶层窗口，还有 limits[]（模型专属周额度）与 extra_usage。
// 这些是"读得到但读不全"的典型：只解析顶层窗口，用户就看到 Opus 还剩 80%，
// 实际它自己的周额度已经用掉 95%。
func TestQueryQuotaParsesScopedLimitsAndExtraUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 10, "resets_at": "2026-11-04T15:00:00Z"},
			"seven_day": {"utilization": 55, "resets_at": "2026-11-10T00:00:00Z"},
			"seven_day_fable": {"utilization": 3, "resets_at": "2026-11-10T00:00:00Z"},
			"limits": [
				{"kind":"session","percent":0,"resets_at":"2026-11-04T15:00:00Z"},
				{"kind":"weekly","percent":55},
				{"kind":"weekly_scoped","group":"weekly","percent":95,"scope":{"model":{"display_name":"Opus 4.6","id":"claude-opus-4-6"}}},
				{"kind":"weekly_scoped","group":"weekly","percent":12,"scope":{"model":{"id":"claude-sonnet-4-6"}}}
			],
			"extra_usage": {"monthly_limit": 50, "used_credits": 12.5, "currency": "usd"}
		}`))
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("a1", channelID)
	env.SetCredential("access_token", "sk-ant-oat01-test")
	env.SetCredential("base_url", srv.URL)
	env.HTTPClient = srv.Client()

	q, err := (&Provider{}).QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}

	byName := map[string]provider.QuotaWindow{}
	for _, w := range q.Windows {
		byName[w.Name] = w
	}
	if w, ok := byName["Weekly · Opus 4.6"]; !ok || w.Used != 95 {
		t.Fatalf("应读出模型专属周额度（Opus 4.6 = 95%%），得到 %+v", q.Windows)
	}
	// 没有 display_name 时退回模型 id，不能丢窗口。
	if w, ok := byName["Weekly · claude-sonnet-4-6"]; !ok || w.Used != 12 {
		t.Fatalf("没有 display_name 时应退回 id，得到 %+v", q.Windows)
	}
	// session / weekly 这两个 limit 与顶层窗口重复，不能重复计入。
	if _, ok := byName["Weekly"]; !ok {
		t.Fatalf("顶层 Weekly 应保留")
	}
	if len(q.Windows) != 6 {
		t.Fatalf("窗口数 = %d（5h/Weekly/Fable/2 个 scoped/套餐外 = 6），得到 %+v", len(q.Windows), q.Windows)
	}
	// extra_usage：12.5 / 50 = 25%
	if w, ok := byName["套餐外用量（USD）"]; !ok || w.Used != 25 {
		t.Fatalf("套餐外用量应为 25%%，得到 %+v", q.Windows)
	}
}

// 用量为 0 时不展示占位 resets_at：否则会出现"0% 却 2 小时后重置"。
func TestUsageZeroDropsPlaceholderReset(t *testing.T) {
	if got := usageReset(0, "2026-11-04T15:00:00Z"); got != 0 {
		t.Fatalf("用量 0 时应丢弃重置时间，得到 %d", got)
	}
	if got := usageReset(12, 1793102400.0); got != 1793102400 {
		t.Fatalf("有用量时应保留重置时间，得到 %d", got)
	}
}
