package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

func TestUsageStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	s := newUsageStore(path)
	today := usageDate(time.Now())
	s.record(usageCell{Date: today, ProviderID: "p1", Model: "m1", AccountID: "a1",
		Requests: 2, InputTokens: 100, OutputTokens: 50, DurationMS: 900})
	s.record(usageCell{Date: today, ProviderID: "p1", Model: "m1", AccountID: "a1",
		Requests: 1, Errors: 1, InputTokens: 10, OutputTokens: 5})
	s.flush()

	s2 := newUsageStore(path)
	day, ok := s2.days[today]
	if !ok {
		t.Fatal("重启后数据丢失")
	}
	c := day[cellKey("p1", "m1", "a1", "", "")]
	if c == nil || c.Requests != 3 || c.Errors != 1 || c.InputTokens != 110 || c.OutputTokens != 55 {
		t.Fatalf("聚合错误: %+v", c)
	}
}

func TestUsageAddTokens(t *testing.T) {
	c := &usageCell{}
	// 统一口径：total 应等于 in + cached + cacheWrite + out。
	// 这里 total(20) 比各类之和(18) 多 2，多出的部分补进普通输入，避免总量丢失。
	c.addTokens(&ir.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 20, CachedTokens: 3, ReasoningTokens: 2})
	if c.InputTokens != 12 {
		t.Errorf("InputTokens = %d, want 12", c.InputTokens)
	}
	if c.OutputTokens != 5 || c.CachedTokens != 3 || c.ReasoningTokens != 2 {
		t.Errorf("字段不对: %+v", c)
	}

	// cache 写入单独成列，不再混进普通输入。
	c2 := &usageCell{}
	c2.addTokens(&ir.Usage{InputTokens: 4, OutputTokens: 1, CachedTokens: 2, CacheCreationTokens: 6})
	if c2.InputTokens != 4 || c2.CachedTokens != 2 || c2.CacheCreate != 6 {
		t.Errorf("cache 写入未单列: %+v", c2)
	}
}

func TestCostOfAppliesCacheRates(t *testing.T) {
	p := ModelPrice{InputPerM: 3, OutputPerM: 15, CachedReadPerM: 0.3, CachedWritePerM: 3.75}
	// 1M 普通输入 = 3；1M 输出 = 15；1M cache 读 = 0.3；1M cache 写 = 3.75
	got := costOf(p, 1_000_000, 1_000_000, 1_000_000, 1_000_000)
	if want := 3 + 15 + 0.3 + 3.75; got < want-1e-9 || got > want+1e-9 {
		t.Errorf("cost = %v, want %v", got, want)
	}
	// 未配置 cache 单价时回落普通输入价，不能算成 0。
	plain := ModelPrice{InputPerM: 2, OutputPerM: 8}
	if got := costOf(plain, 0, 0, 1_000_000, 1_000_000); got != 4 {
		t.Errorf("回落单价错误: %v", got)
	}
}

func TestUsagePriceFor(t *testing.T) {
	g := &Gateway{}
	g.settings = runtimeSettings{
		ModelPrices: map[string]ModelPrice{
			"antigravity/claude-opus-4-6-thinking": {InputPerM: 5, OutputPerM: 25},
			"antigravity/claude-*":                 {InputPerM: 3, OutputPerM: 15},
			"*":                                    {InputPerM: 1, OutputPerM: 2},
		},
	}
	cases := []struct {
		model string
		want  ModelPrice
	}{
		{"antigravity/claude-opus-4-6-thinking", ModelPrice{InputPerM: 5, OutputPerM: 25}},
		{"antigravity/claude-sonnet-4-6", ModelPrice{InputPerM: 3, OutputPerM: 15}},
		{"anything/else", ModelPrice{InputPerM: 1, OutputPerM: 2}},
	}
	for _, c := range cases {
		got, ok := g.priceFor(c.model)
		if !ok || got.InputPerM != c.want.InputPerM || got.OutputPerM != c.want.OutputPerM {
			t.Errorf("priceFor(%q) = %+v (ok=%v), want %+v", c.model, got, ok, c.want)
		}
	}
}

func TestUsageQueryAPI(t *testing.T) {
	g := &Gateway{}
	g.usage = newUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	g.settings = runtimeSettings{
		ModelPrices: map[string]ModelPrice{"m1": {InputPerM: 1, OutputPerM: 2}},
	}
	g.usage.record(usageCell{
		Date: usageDate(time.Now()), ProviderID: "p1", Model: "m1", AccountID: "a1",
		Requests: 4, InputTokens: 1_000_000, OutputTokens: 1_000_000,
	})

	req := httptest.NewRequest("GET", "/api/usage?days=7", nil)
	rec := httptest.NewRecorder()
	g.handleUsage(rec, req)

	var resp struct {
		Days   []usageDayQuery `json:"days"`
		Totals struct {
			Requests    int64   `json:"requests"`
			InputTokens int64   `json:"input_tokens"`
			Cost        float64 `json:"cost"`
		} `json:"totals"`
		Models []usageModelRow `json:"models_native"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v", err)
	}
	if resp.Totals.Requests != 4 {
		t.Errorf("Requests = %d, want 4", resp.Totals.Requests)
	}
	if resp.Totals.InputTokens != 1_000_000 {
		t.Errorf("InputTokens = %d, want 1000000", resp.Totals.InputTokens)
	}
	// 1M 输入 * $1 + 1M 输出 * $2 = $3
	if resp.Totals.Cost < 2.999 || resp.Totals.Cost > 3.001 {
		t.Errorf("Cost = %v, want 3.0", resp.Totals.Cost)
	}
	if len(resp.Days) != 7 {
		t.Errorf("Days = %d, want 7", len(resp.Days))
	}
	if len(resp.Models) != 1 || resp.Models[0].Model != "m1" {
		t.Errorf("Models 不对: %+v", resp.Models)
	}
}

// 客户端中途断开时，上游末尾的 usage 仍必须被记录——
// 否则所有被用户取消的长回答都不计费，账目会系统性偏低。
func TestUsageRecorderDrainsAfterClientAbort(t *testing.T) {
	store := newUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	g := &Gateway{usage: store}

	ctx, cancel := context.WithCancel(context.Background())
	upstream := make(chan ir.UnifiedStreamEvent, 8)
	upstream <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "hi"}

	out := g.withUsageRecorder(ctx, upstream, usageCell{
		Date: usageDate(time.Now()), ProviderID: "p1", Model: "m1", AccountID: "a1",
	}, nil, "", nil)

	<-out
	cancel() // 客户端断开

	go func() {
		time.Sleep(30 * time.Millisecond)
		upstream <- ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: &ir.Usage{
			InputTokens: 7, OutputTokens: 3, TotalTokens: 10,
		}}
		close(upstream)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		store.mu.Lock()
		cell := store.days[usageDate(time.Now())][cellKey("p1", "m1", "a1", "", "")]
		store.mu.Unlock()
		if cell != nil && cell.InputTokens == 7 && cell.OutputTokens == 3 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("断流后的 usage 未被记录: %+v", cell)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
