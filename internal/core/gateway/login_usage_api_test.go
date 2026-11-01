package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/provider"

	_ "aether-cockpit/internal/providers/claude"
	_ "aether-cockpit/internal/providers/openaicompat"
)

// login/usage 的契约（GAP §228）：响应是 user → quota 映射，quota 字段与
// 额度卡同构（windows[{name,used,resetsAt}] / plan / resets / asOf / readAt）。
func TestLoginUsageProjectsQuotaCards(t *testing.T) {
	g := &Gateway{pool: pool.New()}

	a := provider.NewAccountEnv("acc-a", "openai-compat")
	a.Label = "me@example.com"
	a.SetQuota(&provider.QuotaStatus{
		Plan: "Pro",
		Windows: []provider.QuotaWindow{
			{Name: "5 hours", Used: 42, ResetsAt: time.Now().Add(3 * time.Hour).Unix()},
		},
		Resets: &provider.QuotaResets{Count: 2},
		ReadAt: time.Now().Unix(),
	})
	g.pool.Add(a)

	b := provider.NewAccountEnv("acc-b", "claude-sub")
	b.Label = "claude-user"
	b.SetQuota(&provider.QuotaStatus{
		Windows: []provider.QuotaWindow{{Name: "Weekly", Used: 7}},
		ReadAt:  time.Now().Unix(),
	})
	g.pool.Add(b)

	rec := httptest.NewRecorder()
	g.handleLoginUsage(rec, httptest.NewRequest(http.MethodGet, "/api/login/usage", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]struct {
		Provider string `json:"provider"`
		Plan     string `json:"plan"`
		Windows  []struct {
			Name     string  `json:"name"`
			Used     float64 `json:"used"`
			ResetsAt string  `json:"resetsAt"`
		} `json:"windows"`
		Resets *struct {
			Count int `json:"count"`
		} `json:"resets"`
		ReadAt int64 `json:"readAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("不是 user→quota 映射: %v: %s", err, rec.Body.String())
	}
	if len(out) != 2 {
		t.Fatalf("用户数 = %d, want 2: %s", len(out), rec.Body.String())
	}
	a1, ok := out["me@example.com"]
	if !ok {
		t.Fatalf("缺少 me@example.com 的额度: %s", rec.Body.String())
	}
	if a1.Plan != "Pro" || len(a1.Windows) != 1 || a1.Windows[0].Used != 42 || a1.Windows[0].Name != "5 hours" {
		t.Errorf("quota 投影不对: %+v", a1)
	}
	if !json.Valid([]byte(`"`+a1.Windows[0].ResetsAt+`"`)) || len(a1.Windows[0].ResetsAt) < 4 {
		t.Errorf("resetsAt 应为 ISO 字符串: %q", a1.Windows[0].ResetsAt)
	}
	if a1.Resets == nil || a1.Resets.Count != 2 {
		t.Errorf("resets.count 丢失: %+v", a1.Resets)
	}
	if a1.ReadAt == 0 {
		t.Error("readAt 应为毫秒时间戳")
	}
}

// agent 过滤：接受客户端 id / magpie 订阅 id / 渠道 id 三种写法。
func TestLoginUsageAgentFilter(t *testing.T) {
	g := &Gateway{pool: pool.New()}
	a := provider.NewAccountEnv("acc-a", "claude-sub")
	a.Label = "claude-user"
	a.SetQuota(&provider.QuotaStatus{
		Windows: []provider.QuotaWindow{{Name: "5 hours", Used: 10}},
		ReadAt:  time.Now().Unix(),
	})
	g.pool.Add(a)
	b := provider.NewAccountEnv("acc-b", "openai-compat")
	b.Label = "relay-user"
	b.SetQuota(&provider.QuotaStatus{
		Windows: []provider.QuotaWindow{{Name: "5 hours", Used: 10}},
		ReadAt:  time.Now().Unix(),
	})
	g.pool.Add(b)

	call := func(agent string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		g.handleLoginUsage(rec, httptest.NewRequest(http.MethodGet, "/api/login/usage?agent="+agent, nil))
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("agent=%s 解析失败: %v", agent, err)
		}
		return out
	}

	if got := call("claude_code"); len(got) != 1 || got["claude-user"] == nil {
		t.Errorf("claude_code 应只返回 claude-sub 账号: %v", got)
	}
	if got := call("claude"); len(got) != 1 || got["claude-user"] == nil {
		t.Errorf("claude 别名应映射到 claude-sub: %v", got)
	}
	if got := call("openai-compat"); len(got) != 1 || got["relay-user"] == nil {
		t.Errorf("直接传渠道 id 应生效: %v", got)
	}
	if got := call("nope"); len(got) != 0 {
		t.Errorf("未知 agent 应返回空映射，而不是全部: %v", got)
	}
}

// provider/move 没有真实语义：必须显式 501 + 原因，不能假成功。
func TestProviderMoveIs501(t *testing.T) {
	g := &Gateway{pool: pool.New()}
	rec := httptest.NewRecorder()
	g.handleProviderMove(rec, httptest.NewRequest(http.MethodPost, "/api/provider/move", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if body.Code != "no_plugin_packages" || body.Error == "" {
		t.Fatalf("501 必须带可执行原因: %+v", body)
	}
}

// provider/unfetch：服务端不持久化 vendor 清单，返回最新完整视图（幂等）。
func TestProviderUnfetchReturnsProvidersView(t *testing.T) {
	g := New(Config{Addr: "127.0.0.1:0"})
	rec := httptest.NewRecorder()
	g.handleProviderUnfetch(rec, httptest.NewRequest(http.MethodPost, "/api/provider/unfetch", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if _, ok := body["providers"]; !ok {
		t.Fatalf("应返回完整 providers 视图: %s", rec.Body.String())
	}
}
