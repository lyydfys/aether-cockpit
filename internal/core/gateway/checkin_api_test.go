package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/scheduler"
	"aether-cockpit/internal/provider"
)

func newCheckinTestGateway(t *testing.T) *Gateway {
	t.Helper()
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	s := scheduler.New(scheduler.DefaultOptions(), g.Pool(), nil)
	g.SetScheduler(s)
	t.Cleanup(s.Stop)
	return g
}

func TestCheckinPreferenceDefaultOff(t *testing.T) {
	g := newCheckinTestGateway(t)
	if g.checkinPrefOn("workbuddy") {
		t.Fatal("签到开关默认应关闭")
	}
	g.settingsMu.Lock()
	g.settings.UI = map[string]any{"workbuddyCheckin": true}
	g.settingsMu.Unlock()
	if !g.checkinPrefOn("workbuddy") {
		t.Fatal("保存后应打开")
	}
	// /api/state 的透传路径用短横线键名。
	g.settingsMu.Lock()
	g.settings.UI = map[string]any{"qoder-checkin": map[string]any{"on": true}}
	g.settingsMu.Unlock()
	if !g.checkinPrefOn("qoder") {
		t.Fatal("短横线键名也应识别")
	}
	if g.checkinPrefOn("minimax") {
		t.Fatal("未设置的厂商应保持关闭")
	}
}

func TestCheckinVendorMapping(t *testing.T) {
	cases := map[string]string{
		"workbuddy-cn": "", "trae-cn": "trae", "trae-solo-cn": "trae",
		"qoder-cn": "qoder", "qoder-intl": "qoder", "minimax-code": "minimax",
		"minimax-code-global": "minimax",
	}
	for id, want := range cases {
		got, ok := checkinVendor(id)
		if !ok || got != want {
			t.Fatalf("%s → %q/%v, want %q", id, got, ok, want)
		}
	}
	if _, ok := checkinVendor("workbuddy-intl"); ok {
		t.Fatal("workbuddy-intl 没有签到")
	}
}

func TestTodayCheckinCard(t *testing.T) {
	g := newCheckinTestGateway(t)
	s := g.Scheduler()
	now := time.Now()
	s.State().Record("acc-1", "daily_checkin", scheduler.Result{
		Success: true, Outcome: provider.CheckinClaimed, Credit: 100, Streak: 3,
		Day: provider.CheckinDay(now), At: now.Unix(),
	}, scheduler.DayKey(now))

	c := g.todayCheckinCard("acc-1")
	if c == nil || c.Outcome != provider.CheckinClaimed || c.Credit != 100 || c.Streak != 3 {
		t.Fatalf("card = %+v", c)
	}
	if g.todayCheckinCard("acc-2") != nil {
		t.Fatal("没有记录的账号不应有签到卡片")
	}
}

func TestCheckinProvisional(t *testing.T) {
	// 2026-03-01 03:00 UTC = 11:00 Beijing → 可能还是厂商前一天。
	early := time.Date(2026, 3, 1, 3, 0, 0, 0, time.UTC).Unix()
	late := time.Date(2026, 3, 1, 5, 0, 0, 0, time.UTC).Unix() // 13:00 Beijing
	if !checkinProvisional(provider.CheckinDone, early) {
		t.Fatal("中午前的 done 应标记 again")
	}
	if checkinProvisional(provider.CheckinDone, late) {
		t.Fatal("中午后的 done 不应再问")
	}
	if checkinProvisional(provider.CheckinClaimed, early) {
		t.Fatal("claimed 不是待确认答案")
	}
}

func TestCheckinPressEndpoints(t *testing.T) {
	g := newCheckinTestGateway(t)

	rec := httptest.NewRecorder()
	g.handleUsageCheckIn("minimax")(rec, httptest.NewRequest(http.MethodPost, "/api/usage/minimax-checkin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("minimax = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	g.handleUsageCheckIn("workbuddy")(rec, httptest.NewRequest(http.MethodPost, "/api/usage/workbuddy-checkin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("workbuddy = %d %s", rec.Code, rec.Body.String())
	}
	var out []checkinPressResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("没有账号时应返回空数组: %+v", out)
	}
}
