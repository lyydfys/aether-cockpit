package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/provider"

	_ "aether-cockpit/internal/providers/openaicompat"
)

// 用量页的契约：series / agents / accounts 必须是数组，缺一个前端就空态。
func TestUsageAPIContract(t *testing.T) {
	g := &Gateway{}
	g.usage = newUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	g.settings = runtimeSettings{
		ModelPrices: map[string]ModelPrice{"m1": {InputPerM: 1, OutputPerM: 2}},
	}
	today := usageDate(time.Now())
	g.usage.record(usageCell{
		Date: today, ProviderID: "p1", Model: "m1", AccountID: "a1", Client: "claude-code",
		Requests: 2, InputTokens: 100, OutputTokens: 50,
	})
	g.usage.record(usageCell{
		Date: today, ProviderID: "p1", Model: "m1", AccountID: "a2", Client: "codex",
		Requests: 1, InputTokens: 10, OutputTokens: 5,
	})
	g.usage.record(usageCell{
		Date: today, ProviderID: "p9", Model: "unpriced-model", AccountID: "a9",
		Requests: 3, InputTokens: 1,
	})

	req := httptest.NewRequest("GET", "/api/usage?period=today", nil)
	rec := httptest.NewRecorder()
	g.handleUsage(rec, req)

	var resp struct {
		Calls    int64            `json:"calls"`
		Unpriced int64            `json:"unpriced"`
		Series   []map[string]any `json:"series"`
		Bucket   string           `json:"bucket"`
		Agents   []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Calls int    `json:"calls"`
		} `json:"agents"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
		Accounts []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"accounts"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp.Calls != 6 {
		t.Errorf("calls = %d, want 6", resp.Calls)
	}
	if resp.Unpriced != 3 {
		t.Errorf("unpriced = %d, want 3", resp.Unpriced)
	}
	if resp.Bucket != "day" || len(resp.Series) != 1 {
		t.Errorf("series/bucket 不对: bucket=%q n=%d", resp.Bucket, len(resp.Series))
	}
	if len(resp.Agents) != 3 { // claude-code / codex / other
		t.Errorf("agents = %+v, want 3 rows", resp.Agents)
	}
	foundClaude := false
	for _, a := range resp.Agents {
		if a.ID == "claude-code" && a.Name == "Claude Code" && a.Calls == 2 {
			foundClaude = true
		}
	}
	if !foundClaude {
		t.Errorf("Claude Code 行缺失: %+v", resp.Agents)
	}
	if len(resp.Models) != 2 {
		t.Errorf("models = %d, want 2", len(resp.Models))
	}
	if len(resp.Accounts) != 3 || resp.Accounts[0].Name == "" {
		t.Errorf("accounts 不对: %+v", resp.Accounts)
	}
	if resp.Path == "" {
		t.Error("path 为空")
	}
}

// 额度卡片：有窗口的账号必须给出 cards，且 resetsAt 是 ISO（前端 new Date 解析）。
func TestUsageQuotasCards(t *testing.T) {
	g := &Gateway{pool: pool.New()}
	env := provider.NewAccountEnv("acc-1", "openai-compat")
	env.Label = "主账号"
	reset := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	env.SetQuota(&provider.QuotaStatus{
		Windows: []provider.QuotaWindow{
			{Name: "5 hours", Used: 42, ResetsAt: reset.Unix()},
			{Name: "Weekly", Used: 7, ResetsAt: reset.Add(48 * time.Hour).Unix()},
		},
		ReadAt: time.Now().Unix(),
	})
	g.pool.Add(env)

	req := httptest.NewRequest("GET", "/api/usage/quotas", nil)
	rec := httptest.NewRecorder()
	g.handleUsageQuotas(rec, req)

	var cards []struct {
		Provider string `json:"provider"`
		Name     string `json:"name"`
		User     string `json:"user"`
		Windows  []struct {
			Name     string  `json:"name"`
			Used     float64 `json:"used"`
			ResetsAt string  `json:"resetsAt"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cards); err != nil {
		t.Fatalf("解析失败: %v: %s", err, rec.Body.String())
	}
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want 1: %s", len(cards), rec.Body.String())
	}
	c := cards[0]
	if c.Provider != "openai-compat" || c.User != "主账号" || c.Name == "" {
		t.Errorf("卡片身份不对: %+v", c)
	}
	if len(c.Windows) != 2 || c.Windows[0].Name != "5 hours" || c.Windows[0].Used != 42 {
		t.Errorf("窗口不对: %+v", c.Windows)
	}
	if !strings.HasPrefix(c.Windows[0].ResetsAt, "20") {
		t.Errorf("resetsAt 不是 ISO: %q", c.Windows[0].ResetsAt)
	}
}

// 没有用量接口的渠道：429 观测窗口要能兜底成卡片。
func TestUsageQuotasFromRateLimitObservation(t *testing.T) {
	g := &Gateway{pool: pool.New()}
	env := provider.NewAccountEnv("acc-2", "openai-compat")
	env.Label = "限流账号"
	env.NoteRateLimit(3*time.Hour, time.Now())
	g.pool.Add(env)

	cards, _ := g.usageQuotaCards()
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(cards))
	}
	if len(cards[0].Windows) != 1 || cards[0].Windows[0].Name != "5 hours" {
		t.Fatalf("429 窗口未成卡: %+v", cards[0])
	}
	if cards[0].Windows[0].Used != 100 {
		t.Errorf("used = %v, want 100", cards[0].Windows[0].Used)
	}
}

// 池/家族字段必须原样出现在卡片 JSON 里：前端靠 pool/family/members
// 画「Gemini 5h / 7d」与模型族的分组条。
func TestUsageQuotasPoolFieldsPreserved(t *testing.T) {
	g := &Gateway{pool: pool.New()}
	env := provider.NewAccountEnv("acc-pool", "openai-compat")
	env.Label = "agy"
	env.SetQuota(&provider.QuotaStatus{
		Windows: []provider.QuotaWindow{
			{Name: "5 hours", Used: 10, Pool: "Gemini"},
			{Name: "gemini-3-flash", Used: 0, Pool: "Gemini", Family: "Gemini"},
		},
		ReadAt: time.Now().Unix(),
	})
	g.pool.Add(env)

	cards, _ := g.usageQuotaCards()
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(cards))
	}
	body, _ := json.Marshal(cards[0])
	s := string(body)
	if !strings.Contains(s, `"pool":"Gemini"`) || !strings.Contains(s, `"family":"Gemini"`) {
		t.Fatalf("池/家族字段丢失: %s", s)
	}
}

// 读取失败且没有旧读数时：出现带原因的卡，而不是静默消失。
func TestUsageQuotasFailureCard(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	// httptest 默认 mux 对未注册路径回 404：账单接口必然读不到。
	env := provider.NewAccountEnv("acc-fail", "openai-compat")
	env.Label = "死中继"
	env.Credentials = map[string]string{"base_url": srv.URL, "api_key": "k"}
	env.HTTPClient = srv.Client()

	g := &Gateway{pool: pool.New()}
	g.pool.Add(env)

	if err := g.readQuotaFor(env, 3*time.Second); err == nil {
		t.Fatal("账单接口 404 应当返回错误")
	}
	cards, _ := g.usageQuotaCards()
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(cards))
	}
	if cards[0].Error == "" || !strings.Contains(cards[0].Error, "账单接口") {
		t.Fatalf("失败卡 = %+v", cards[0])
	}
	if len(cards[0].Windows) != 0 {
		t.Fatalf("失败卡不应有窗口: %+v", cards[0].Windows)
	}
}

// Codex 重置卡数量要出现在卡片 JSON（前端 q.resets.count）。
func TestUsageQuotasResetsCount(t *testing.T) {
	g := &Gateway{pool: pool.New()}
	env := provider.NewAccountEnv("acc-reset", "openai-compat")
	env.Label = "重置卡"
	env.SetQuota(&provider.QuotaStatus{
		Windows: []provider.QuotaWindow{{Name: "5 hours", Used: 10}},
		Resets:  &provider.QuotaResets{Count: 3},
		ReadAt:  time.Now().Unix(),
	})
	g.pool.Add(env)
	cards, _ := g.usageQuotaCards()
	if len(cards) != 1 || cards[0].Resets == nil || cards[0].Resets.Count != 3 {
		t.Fatalf("cards = %+v", cards)
	}
	b, _ := json.Marshal(cards[0])
	if !strings.Contains(string(b), `"resets":{"count":3}`) {
		t.Fatalf("json = %s", b)
	}
}
