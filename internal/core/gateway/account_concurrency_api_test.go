package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestAccountConcurrencyOverride(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		UsagePath:        "-",
	}
	g := New(cfg)
	env := provider.NewAccountEnv("conc-acc", "mock-native")
	env.Label = "conc"
	g.pool.Add(env)
	g.saveAccountsNow()

	post := func(body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		g.handleProviderAccountConcurrency(rec, httptest.NewRequest(http.MethodPost, "/api/provider/accountconcurrency", strings.NewReader(body)))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	if code, _ := post(`{"id":"mock-native","account":"conc-acc","limit":2}`); code != http.StatusOK {
		t.Fatalf("设置并发 = %d", code)
	}
	if v, ok := env.ConcurrencyOverride(); !ok || v != 2 {
		t.Fatalf("override = %d,%v", v, ok)
	}
	if got := g.concurrencyFor(env); got != 2 {
		t.Fatalf("生效并发 = %d", got)
	}
	// providers 回显：accountConcurrency[accountID] 与 maxConcurrency。
	rec := httptest.NewRecorder()
	g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	var envelope struct {
		Providers []struct {
			ID                 string         `json:"id"`
			MaxConcurrency     int            `json:"maxConcurrency"`
			AccountConcurrency map[string]int `json:"accountConcurrency"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range envelope.Providers {
		if p.ID == "mock-native" {
			found = true
			if p.AccountConcurrency["conc-acc"] != 2 {
				t.Fatalf("accountConcurrency = %v", p.AccountConcurrency)
			}
		}
	}
	if !found {
		t.Fatal("providers 未列出 mock-native")
	}
	// 落盘 + 重启恢复。
	raw, err := os.ReadFile(filepath.Join(dir, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"concurrency": 2`) {
		t.Fatalf("并发覆盖未落盘: %s", raw)
	}
	g2 := New(cfg)
	env2, ok := g2.pool.Get("conc-acc")
	if !ok {
		t.Fatal("账号未恢复")
	}
	if v, ok := env2.ConcurrencyOverride(); !ok || v != 2 {
		t.Fatalf("重启后 override = %d,%v", v, ok)
	}

	// null 清除，回到全局默认（cfg 未设 → 8）。
	if code, _ := post(`{"id":"mock-native","account":"conc-acc","limit":null}`); code != http.StatusOK {
		t.Fatalf("清除并发 = %d", code)
	}
	if _, ok := env.ConcurrencyOverride(); ok {
		t.Fatal("清除后不应再有覆盖")
	}
	if got := g.concurrencyFor(env); got != 8 {
		t.Fatalf("清除后应跟随全局 8，得到 %d", got)
	}
	// 0 = 不限制。
	post(`{"id":"mock-native","account":"conc-acc","limit":0}`)
	if got := g.concurrencyFor(env); got != 0 {
		t.Fatalf("0 应为不限制，得到 %d", got)
	}
	// 未知账号 404。
	if code, _ := post(`{"id":"mock-native","account":"nope","limit":1}`); code != http.StatusNotFound {
		t.Fatalf("未知账号 = %d", code)
	}
}

func TestLanesBusyCounts(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	env := provider.NewAccountEnv("lane-acc", "mock-native")
	g.pool.Add(env)

	release, ok := env.TryAcquire(2)
	if !ok {
		t.Fatal("应能占位")
	}
	rec := httptest.NewRecorder()
	g.handleLanes(rec, httptest.NewRequest(http.MethodGet, "/api/lanes", nil))
	var lanes map[string]struct {
		Busy    int `json:"busy"`
		Waiting int `json:"waiting"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lanes); err != nil {
		t.Fatal(err)
	}
	lane, ok := lanes["mock-native#lane-acc"]
	if !ok || lane.Busy != 1 || lane.Waiting != 0 {
		t.Fatalf("lanes = %v", lanes)
	}
	release()
	rec = httptest.NewRecorder()
	g.handleLanes(rec, httptest.NewRequest(http.MethodGet, "/api/lanes", nil))
	_ = json.Unmarshal(rec.Body.Bytes(), &lanes)
	if lanes["mock-native#lane-acc"].Busy != 0 {
		t.Fatalf("释放后 busy 应为 0: %v", lanes)
	}
}
