package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func f64(v float64) *float64 { return &v }

// 渠道计费倍率：保存 / 回显 / 清除 / 越界 400。
func TestProviderPriceRateSaveEcho(t *testing.T) {
	g := headersGateway(t)
	if rec := saveHeaders(t, g, `{"id":"mock-native","priceRate":0.8}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	if v := providerViewOf(t, g, "mock-native")["priceRate"]; v != 0.8 {
		t.Fatalf("回显 priceRate = %v，期望 0.8", v)
	}
	// 1 等于官方价：清除条目。
	if rec := saveHeaders(t, g, `{"id":"mock-native","priceRate":1}`); rec.Code != http.StatusOK {
		t.Fatalf("save 1 status = %d", rec.Code)
	}
	if v := providerViewOf(t, g, "mock-native")["priceRate"]; v != float64(0) {
		t.Fatalf("1 应清除倍率，got %v", v)
	}
	for _, body := range []string{
		`{"id":"mock-native","priceRate":-0.1}`,
		`{"id":"mock-native","priceRate":1001}`,
	} {
		if rec := saveHeaders(t, g, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400，got %d", body, rec.Code)
		}
	}
}

// 用量聚合按渠道倍率折算：p1 0.8×、p2 官方价，totals 与按渠道行都要对。
func TestUsageCostAppliesPriceRate(t *testing.T) {
	g := &Gateway{}
	g.usage = newUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	g.settings = runtimeSettings{
		ModelPrices:       map[string]ModelPrice{"m1": {InputPerM: 1}},
		ProviderPriceRate: map[string]float64{"p1": 0.8},
	}
	today := usageDate(time.Now())
	for _, pid := range []string{"p1", "p2"} {
		g.usage.record(usageCell{
			Date: today, ProviderID: pid, Model: "m1", AccountID: pid + "-a",
			Requests: 1, InputTokens: 1_000_000,
		})
	}

	rec := httptest.NewRecorder()
	g.handleUsage(rec, httptest.NewRequest(http.MethodGet, "/api/usage?days=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("usage status = %d", rec.Code)
	}
	var resp struct {
		Days []struct {
			ByProvider map[string]struct {
				Cost float64 `json:"cost"`
			} `json:"by_provider"`
		} `json:"days"`
		Totals struct {
			Cost float64 `json:"cost"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(resp.Days) != 1 {
		t.Fatalf("days = %d", len(resp.Days))
	}
	by := resp.Days[0].ByProvider
	if got := by["p1"].Cost; got != 0.8 {
		t.Fatalf("p1 应按 0.8× 折算为 0.8，got %v", got)
	}
	if got := by["p2"].Cost; got != 1 {
		t.Fatalf("p2 应为官方价 1，got %v", got)
	}
	if got := resp.Totals.Cost; got != 1.8 {
		t.Fatalf("totals.Cost = %v，期望 1.8", got)
	}

	_ = f64(1)
}
