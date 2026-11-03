package gateway

import (
	"testing"
	"time"

	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/provider"
)

// newAlertGateway 造一个只装了账号池的网关（额度巡检只看 pool）。
func newAlertGateway(t *testing.T, envs ...*provider.AccountEnv) *Gateway {
	t.Helper()
	dir := t.TempDir()
	g := &Gateway{
		settingsPath: dir + "/settings.json",
		pool:         pool.New(),
		callerKeys:   newCallerKeyStore(dir + "/caller-keys.json"),
	}
	for _, e := range envs {
		g.pool.Add(e)
	}
	return g
}

func alertEnv(t *testing.T, providerID, accountID string, windows ...provider.QuotaWindow) *provider.AccountEnv {
	t.Helper()
	e := provider.NewAccountEnv(accountID, providerID)
	e.Label = accountID
	e.SetQuota(&provider.QuotaStatus{Windows: windows})
	return e
}

// 「还剩一大半但马上重置」是最值钱的一条提醒：花钱买的额度要作废了。
func TestQuotaAlertExpiringBeforeReset(t *testing.T) {
	now := time.Now()
	e := alertEnv(t, "claude-code", "acc1", provider.QuotaWindow{
		Name: "5h", Used: 20, ResetsAt: now.Add(3 * time.Hour).Unix(),
	})
	g := newAlertGateway(t, e)

	alerts := g.collectQuotaAlerts(now)
	if len(alerts) != 1 {
		t.Fatalf("应产出 1 条提醒，得到 %d：%+v", len(alerts), alerts)
	}
	if alerts[0].Kind != "expiring" {
		t.Fatalf("类型应为 expiring，得到 %q", alerts[0].Kind)
	}
	if alerts[0].Key == "" || alerts[0].Message == "" {
		t.Fatalf("提醒必须有去重键与文案：%+v", alerts[0])
	}
}

// 剩得很多但离重置还早 → 不提醒（否则天天吵）。
func TestQuotaAlertNoReminderWhenResetFarAway(t *testing.T) {
	now := time.Now()
	e := alertEnv(t, "claude-code", "acc1", provider.QuotaWindow{
		Name: "5h", Used: 20, ResetsAt: now.Add(48 * time.Hour).Unix(),
	})
	g := newAlertGateway(t, e)
	if alerts := g.collectQuotaAlerts(now); len(alerts) != 0 {
		t.Fatalf("不该提醒：%+v", alerts)
	}
}

// 快用完要告警，用尽单独一类。
func TestQuotaAlertLowAndExhausted(t *testing.T) {
	now := time.Now()
	reset := now.Add(4 * time.Hour).Unix()
	low := alertEnv(t, "claude-code", "low", provider.QuotaWindow{Name: "5h", Used: 90, ResetsAt: reset})
	full := alertEnv(t, "codex", "full", provider.QuotaWindow{Name: "week", Used: 100, ResetsAt: reset})
	g := newAlertGateway(t, low, full)

	kinds := map[string]bool{}
	for _, a := range g.collectQuotaAlerts(now) {
		kinds[a.Kind] = true
	}
	if !kinds["low"] {
		t.Error("90% 已用应产出 low")
	}
	if !kinds["exhausted"] {
		t.Error("100% 已用应产出 exhausted")
	}
}

// 同类提醒在冷却期内只发一次；重置后的告警不再算"当前有效"。
func TestQuotaAlertDedupeAndExpiry(t *testing.T) {
	now := time.Now()
	e := alertEnv(t, "claude-code", "acc1", provider.QuotaWindow{
		Name: "5h", Used: 20, ResetsAt: now.Add(3 * time.Hour).Unix(),
	})
	g := newAlertGateway(t, e)

	g.evaluateQuotaAlerts(now)
	first := len(g.settings.QuotaAlerts)
	if first == 0 {
		t.Fatal("第一次巡检应写入去重记录")
	}
	// 冷却期内再跑：不重复写，也不重复发。
	g.evaluateQuotaAlerts(now.Add(time.Minute))
	if got := len(g.settings.QuotaAlerts); got != first {
		t.Fatalf("冷却期内不该新增去重记录：%d vs %d", got, first)
	}

	// 窗口重置后，ActiveQuotaAlerts 不再列出它。
	e.SetQuota(&provider.QuotaStatus{Windows: []provider.QuotaWindow{{
		Name: "5h", Used: 20, ResetsAt: now.Add(-time.Hour).Unix(),
	}}})
	for _, a := range g.ActiveQuotaAlerts() {
		if a.Window == "5h" {
			t.Fatalf("已重置的窗口不该继续出现在有效提醒里：%+v", a)
		}
	}
}

// 「还剩很多 + 马上到期」必须排在其它告警前面。
func TestQuotaAlertOrdering(t *testing.T) {
	now := time.Now()
	reset := now.Add(2 * time.Hour).Unix()
	low := alertEnv(t, "claude-code", "low", provider.QuotaWindow{Name: "5h", Used: 95, ResetsAt: reset})
	exp := alertEnv(t, "codex", "exp", provider.QuotaWindow{Name: "week", Used: 10, ResetsAt: reset})
	g := newAlertGateway(t, low, exp)

	alerts := g.collectQuotaAlerts(now)
	if len(alerts) < 2 {
		t.Fatalf("应至少有 2 条：%+v", alerts)
	}
	if alerts[0].Kind != "expiring" {
		t.Fatalf("expiring 应排最前，得到 %q", alerts[0].Kind)
	}
}
