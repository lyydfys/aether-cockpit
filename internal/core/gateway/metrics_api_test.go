package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func TestMetricsExposesUsageAndAccounts(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        filepath.Join(t.TempDir(), "usage.json"),
		MaxRecords:       10,
	})
	env := provider.NewAccountEnv("m-acc", "mock-native")
	g.Pool().Add(env)
	g.models.Rebuild()

	// 价格 1 USD / 1M input、渠道倍率 0.5 → 1000 input = 0.001 × 0.5 = 0.0005。
	g.settingsMu.Lock()
	g.settings.ModelPrices = map[string]ModelPrice{"m1": {InputPerM: 1}}
	g.settings.ProviderPriceRate = map[string]float64{"p1": 0.5}
	g.settingsMu.Unlock()
	g.usage.record(usageCell{
		Date: usageDate(time.Now()), ProviderID: "p1", Model: "m1", AccountID: "a1",
		Requests: 2, Errors: 1, InputTokens: 1000, OutputTokens: 500, CachedTokens: 100,
	})

	srv := httptest.NewServer(g)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("Content-Type = %q", ct)
	}
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)

	// 同一指标名不得同时出现"无标签总量"与"带标签分渠道"两条：那样
	// sum(aether_requests_total) 会把同一次请求算两遍。
	for _, name := range []string{"aether_requests_total", "aether_errors_total", "aether_cost_usd_total"} {
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, name+" ") {
				t.Fatalf("%s 不应有不带标签的样本行：%q", name, line)
			}
		}
	}

	for _, want := range []string{
		`aether_requests_total{provider="p1"} 2`,
		`aether_errors_total{provider="p1"} 1`,
		`aether_tokens_total{provider="p1",direction="input"} 1000`,
		`aether_tokens_total{provider="p1",direction="output"} 500`,
		`aether_tokens_total{provider="p1",direction="cached"} 100`,
		`aether_cost_usd_total{provider="p1"} 0.0005`,
		`aether_accounts{provider="mock-native",state="ready"} 1`,
		"aether_models ",
		"aether_uptime_seconds ",
		"# TYPE aether_requests_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("缺少 %q\n---\n%s", want, body)
		}
	}
	// 不泄露提示词/密钥类字段（这里只做最基本的守卫：不应出现 api_key / prompt 字样）。
	if strings.Contains(strings.ToLower(body), "api_key") || strings.Contains(body, "prompt") {
		t.Fatalf("指标里不应出现敏感字段:\n%s", body)
	}
}
