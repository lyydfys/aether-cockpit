package gateway

import (
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func TestDeepSeekPeakOffPeakTier(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:0", SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	peak := time.Date(2026, 10, 30, 10, 0, 0, 0, time.FixedZone("CST", 8*3600)) // Friday 10:00
	off := time.Date(2026, 10, 30, 22, 0, 0, 0, time.FixedZone("CST", 8*3600))  // Friday 22:00
	model := "auto/deepseek-v4.1-flash"
	pPeak, ok := g.priceForUsage(model, 1_000_000, peak)
	if !ok {
		t.Fatal("DeepSeek 默认价格未命中")
	}
	pOff, ok := g.priceForUsage(model, 1_000_000, off)
	if !ok {
		t.Fatal("DeepSeek 平时价格未命中")
	}
	if pPeak.InputPerM <= pOff.InputPerM || pPeak.OutputPerM <= pOff.OutputPerM {
		t.Fatalf("高峰应高于平时: peak=%+v off=%+v", pPeak, pOff)
	}
	if pPeak.InputPerM != pOff.InputPerM*2 {
		t.Fatalf("Flash 高峰输入应为平时 2 倍: %v vs %v", pPeak.InputPerM, pOff.InputPerM)
	}
}

func TestGPTLongContextTier(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:0", SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	at := time.Now()
	short, ok := g.priceForUsage("gpt-5.5", 100_000, at)
	if !ok {
		t.Fatal("GPT-5.5 默认价格未命中")
	}
	long, ok := g.priceForUsage("gpt-5.5", 300_000, at)
	if !ok {
		t.Fatal("GPT-5.5 长上下文价格未命中")
	}
	if long.InputPerM != short.InputPerM*2 {
		t.Fatalf("长上下文输入价应翻倍: short=%v long=%v", short.InputPerM, long.InputPerM)
	}
}

func TestDefaultPricingCoversRoutedModels(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:0", SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	for _, model := range []string{
		"auto/deepseek-v4.1-flash", "workbuddy-intl/deepseek-v4.1-flash-sg",
		"workbuddy-intl/gpt-5.5", "claude-opus-5.5", "gemini-3.1-pro-preview",
		"kimi-k3", "glm-5.3", "minimax-m3", "qwen3.8-max",
	} {
		if _, ok := g.priceFor(model); !ok {
			t.Errorf("默认价格未覆盖 %s", model)
		}
	}
}

func TestCustomPriceOverridesDefault(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:0", SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	g.settingsMu.Lock()
	g.settings.ModelPrices = map[string]ModelPrice{
		"deepseek-v4.1-flash*": {InputPerM: 1, OutputPerM: 2},
	}
	g.settingsMu.Unlock()
	p, ok := g.priceForUsage("auto/deepseek-v4.1-flash", 1_000_000, time.Now())
	if !ok || p.InputPerM != 1 || p.OutputPerM != 2 {
		t.Fatalf("自定义覆盖未生效: ok=%v price=%+v", ok, p)
	}
}

func TestRequestLogGetsTokenAndCost(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        filepath.Join(t.TempDir(), "usage.json"),
	})
	defer g.ledger.Close()
	p := &ledgerPending{rec: ledgerRecord{
		T: time.Now().UnixMilli(), Model: "deepseek-v4.1-flash",
		In: 1000, Out: 100, CacheRead: 500, Status: 200,
	}}
	entry := &RequestLog{Model: "deepseek-v4.1-flash", ProviderID: "workbuddy-intl"}
	p.finalize(g, entry, nil, 200, "", "")
	if entry.InputTokens != 1000 || entry.OutputTokens != 100 || entry.CacheRead != 500 {
		t.Fatalf("日志 token 未回填: %+v", entry)
	}
	if !entry.Priced || entry.Cost <= 0 || entry.PricingModel == "" {
		t.Fatalf("日志费用未回填: %+v", entry)
	}
}

func TestPriceFromCatalogFallback(t *testing.T) {
	g := New(Config{
		Addr: "127.0.0.1:0", SettingsPath: filepath.Join(t.TempDir(), "settings.json"),
		UsagePath: "-", ModelsDir: t.TempDir(),
	})
	g.catalog.metaMu.Lock()
	g.catalog.meta["brandnewmodel"] = metaEntry{CostKnown: true, CostIn: 1.5, CostOut: 4.5, CostCacheRead: 0.1}
	g.catalog.meta["deepseekv41flash"] = metaEntry{CostKnown: true, CostIn: 9, CostOut: 9}
	g.catalog.meta["freemodel"] = metaEntry{CostKnown: true}
	g.catalog.metaMu.Unlock()

	p, ok := g.priceForUsage("qoder-cn/Brand-New-Model", 1000, time.Now())
	if !ok || p.InputPerM != 1.5 || p.OutputPerM != 4.5 || p.CachedReadPerM != 0.1 {
		t.Fatalf("models.dev 兜底价格 = %+v %v", p, ok)
	}
	// 本地价目表命中时优先，不被 models.dev 覆盖。
	q, ok := g.priceForUsage("deepseek-v4.1-flash", 1000, time.Now())
	if !ok || q.InputPerM == 9 {
		t.Fatalf("本地价目表应优先: %+v %v", q, ok)
	}
	// 免费模型（cost 为 0）也应记为已计价，成本 0。
	free, ok := g.priceForUsage("opencode/free-model", 1000, time.Now())
	if !ok || free.InputPerM != 0 || free.OutputPerM != 0 {
		t.Fatalf("免费模型价格 = %+v %v", free, ok)
	}
	// 上游明确标免费（Qoder is_free）时优先于 models.dev 的非零价。
	g.catalog.mu.Lock()
	g.catalog.liveInfo[liveKey("qoder-cn", "free-by-upstream")] = provider.ModelInfo{ID: "free-by-upstream", Free: true}
	g.catalog.mu.Unlock()
	g.catalog.metaMu.Lock()
	g.catalog.meta["freebyupstream"] = metaEntry{CostKnown: true, CostIn: 9, CostOut: 9}
	g.catalog.metaMu.Unlock()
	freeUp, ok := g.priceForUsage("qoder-cn/Free-By-Upstream", 1000, time.Now())
	if !ok || freeUp.InputPerM != 0 || freeUp.OutputPerM != 0 {
		t.Fatalf("上游免费模型价格 = %+v %v", freeUp, ok)
	}
}
