package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// GET /api/network/edge-auto 必须回传"实际生效"的间隔/阈值/采样值。
//
// 历史问题：EdgeAutoConfig 的字段带 omitempty，用户没显式设置时会被序列化成
// 零值省略，前端只知道 enabled=true，不知道重扫是 300 还是别的 ——
// 于是状态行只能显示"上次扫描"，没有"下次什么时候扫"，用户无法判断数据是否陈旧。
func TestEdgeAutoExposesEffectiveConfig(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	g.settingsMu.Lock()
	if g.settings.EdgeAuto == nil {
		g.settings.EdgeAuto = map[string]EdgeAutoConfig{}
	}
	// 只开 enabled，不设其它值：走"默认生效"路径。
	g.settings.EdgeAuto["edge.example.com"] = EdgeAutoConfig{Enabled: true}
	g.settingsMu.Unlock()

	rec := httptest.NewRecorder()
	g.handleEdgeAuto(rec, httptest.NewRequest(http.MethodGet, "/api/network/edge-auto", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// 三个默认值必须出现（300 秒 / 150 ms / 30 秒）。
	for _, want := range []string{`"interval_sec":300`, `"threshold_ms":150`, `"health_sec":30`} {
		if !strings.Contains(body, want) {
			t.Fatalf("响应缺少生效值 %s：%s", want, body)
		}
	}
	if !strings.Contains(body, `"effective":true`) {
		t.Fatalf("应标记这是生效值：%s", body)
	}
}

// 显式配置的值优先于默认值。
func TestEdgeAutoEffectiveConfigRespectsUserValues(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	g.settingsMu.Lock()
	if g.settings.EdgeAuto == nil {
		g.settings.EdgeAuto = map[string]EdgeAutoConfig{}
	}
	g.settings.EdgeAuto["edge.example.com"] = EdgeAutoConfig{
		Enabled: true, IntervalSec: 120, ThresholdMS: 80, HealthSec: 45,
	}
	g.settingsMu.Unlock()

	rec := httptest.NewRecorder()
	g.handleEdgeAuto(rec, httptest.NewRequest(http.MethodGet, "/api/network/edge-auto", nil))
	body := rec.Body.String()
	for _, want := range []string{`"interval_sec":120`, `"threshold_ms":80`, `"health_sec":45`} {
		if !strings.Contains(body, want) {
			t.Fatalf("响应缺少用户配置 %s：%s", want, body)
		}
	}
}

// 越界值要被夹到合法区间（与 interval()/healthInterval() 的口径一致）。
func TestEdgeAutoEffectiveConfigClamps(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	g.settingsMu.Lock()
	if g.settings.EdgeAuto == nil {
		g.settings.EdgeAuto = map[string]EdgeAutoConfig{}
	}
	// 重扫 10 秒 → 夹到 60；采样 1 秒 → 夹到 5。
	g.settings.EdgeAuto["edge.example.com"] = EdgeAutoConfig{
		Enabled: true, IntervalSec: 10, HealthSec: 1,
	}
	g.settingsMu.Unlock()

	rec := httptest.NewRecorder()
	g.handleEdgeAuto(rec, httptest.NewRequest(http.MethodGet, "/api/network/edge-auto", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"interval_sec":60`) {
		t.Fatalf("重扫间隔应夹到 60：%s", body)
	}
	if !strings.Contains(body, `"health_sec":5`) {
		t.Fatalf("采样间隔应夹到 5：%s", body)
	}
}

// 确认默认值本身没变（防止这次改动顺手改了语义）。
func TestEdgeAutoDefaultsUnchanged(t *testing.T) {
	var c EdgeAutoConfig
	if got := c.interval(); got != 300*time.Second {
		t.Fatalf("默认重扫 = %v，want 300s", got)
	}
	if got := c.thresholdMS(); got != 150 {
		t.Fatalf("默认阈值 = %v，want 150", got)
	}
	if got := c.healthInterval(); got != 30*time.Second {
		t.Fatalf("默认采样 = %v，want 30s", got)
	}
}
