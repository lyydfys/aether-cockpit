package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/core/rtstats"
)

// /api/routing-stats 要如实返回"推定口径"的统计：字段齐全、share 是小数、
// 没有样本的渠道不出现（避免界面上出现一堆 0%）。
func TestRoutingStatsAPI(t *testing.T) {
	rtstats.Record("opencode-go", "chatcmpl-a")
	rtstats.Record("opencode-go", "chatcmpl-b")
	rtstats.Record("opencode-go", "chatcmpl-c")
	rtstats.Record("opencode-go", "gw-1")

	g := New(Config{UsagePath: "-"})
	rec := httptest.NewRecorder()
	g.handleRoutingStats(rec, httptest.NewRequest(http.MethodGet, "/api/routing-stats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态 = %d", rec.Code)
	}
	var out struct {
		Providers map[string]struct {
			Total    int64   `json:"total"`
			Official int64   `json:"official"`
			Relay    int64   `json:"relay"`
			Share    float64 `json:"share"`
		} `json:"providers"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	p, ok := out.Providers["opencode-go"]
	if !ok {
		t.Fatalf("缺少渠道统计：%s", rec.Body.String())
	}
	if p.Total != 4 || p.Official != 3 || p.Relay != 1 {
		t.Fatalf("计数不对：%+v", p)
	}
	if p.Share < 0.74 || p.Share > 0.76 {
		t.Fatalf("share = %v，want ≈0.75", p.Share)
	}
	if out.Note == "" {
		t.Fatal("必须带上口径说明，避免把推定值当 SLA")
	}
}
