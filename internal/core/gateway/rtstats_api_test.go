package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/core/rtstats"
)

// /api/routing-stats 要如实返回"推定口径"的统计：渠道级 + **模型级**。
// 模型级不是锦上添花：同渠道里 flash 与 pro 会落在不同后端，只有模型级数字
// 才有意义（界面上的徽章要写成 "DeepSeek V4.1 Flash 官转 96%"）。
func TestRoutingStatsAPI(t *testing.T) {
	rtstats.Record("opencode-go", "deepseek-v4.1-flash", "chatcmpl-a")
	rtstats.Record("opencode-go", "deepseek-v4.1-flash", "chatcmpl-b")
	rtstats.Record("opencode-go", "deepseek-v4.1-flash", "chatcmpl-c")
	rtstats.Record("opencode-go", "deepseek-v4.1-flash", "gw-1")
	rtstats.Record("opencode-go", "deepseek-v4-pro", "chatcmpl-d")

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
			Models   map[string]struct {
				Total    int64   `json:"total"`
				Official int64   `json:"official"`
				Share    float64 `json:"share"`
			} `json:"models"`
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
	// 渠道级是聚合值：4 官方 + 1 中转 = 5 次。
	if p.Total != 5 || p.Official != 4 || p.Relay != 1 {
		t.Fatalf("渠道级计数不对：%+v", p)
	}
	if p.Share < 0.79 || p.Share > 0.81 {
		t.Fatalf("渠道级 share = %v，want ≈0.8", p.Share)
	}
	// 模型级必须拆开：flash 3/4 官方，pro 1/1 官方。
	flash, ok := p.Models["deepseek-v4.1-flash"]
	if !ok {
		t.Fatalf("缺少模型级统计：%s", rec.Body.String())
	}
	if flash.Total != 4 || flash.Official != 3 || flash.Share != 0.75 {
		t.Fatalf("flash 统计不对：%+v", flash)
	}
	pro, ok := p.Models["deepseek-v4-pro"]
	if !ok || pro.Total != 1 || pro.Official != 1 {
		t.Fatalf("pro 统计不对：%+v", p.Models)
	}
	if out.Note == "" {
		t.Fatal("必须带上口径说明，避免把推定值当 SLA")
	}
}
