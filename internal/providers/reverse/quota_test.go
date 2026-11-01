package reverse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func quotaEnvFor(srv *httptest.Server, id string, creds map[string]string) *provider.AccountEnv {
	env := provider.NewAccountEnv(id+"-quota", id)
	env.Credentials = creds
	env.HTTPClient = srv.Client()
	return env
}

func TestReverseRelayQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/dashboard/billing/subscription":
			_ = json.NewEncoder(w).Encode(map[string]any{"hard_limit_usd": 20, "soft_limit_usd": 10})
		case "/dashboard/billing/usage":
			_ = json.NewEncoder(w).Encode(map[string]any{"total_usage": 1250}) // 12.50 美元
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := New(Profile{ID: "codex", QuotaKind: "relay"})
	env := quotaEnvFor(srv, "codex", map[string]string{"base_url": srv.URL + "/v1", "access_token": "sk-test"})
	q, err := p.QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.BalanceUnit != "USD" || q.Balance < 7.49 || q.Balance > 7.51 {
		t.Fatalf("q = %+v", q)
	}
	if !strings.Contains(q.Message, "剩余 $7.50") {
		t.Fatalf("message = %q", q.Message)
	}
}

func TestReverseRelayQuotaHonestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := New(Profile{ID: "codex", QuotaKind: "relay"})
	env := quotaEnvFor(srv, "codex", map[string]string{"base_url": srv.URL, "api_key": "k"})
	if _, err := p.QueryQuota(context.Background(), env); err == nil || !strings.Contains(err.Error(), "账单接口") {
		t.Fatalf("err = %v", err)
	}
}

func TestReverseNoQuotaKind(t *testing.T) {
	p := New(Profile{ID: "kiro"})
	env := provider.NewAccountEnv("a", "kiro")
	if _, err := p.QueryQuota(context.Background(), env); err == nil {
		t.Fatal("没有用量接口的渠道应当报错")
	}
}

func TestOpenCodeGoUsageParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/usage" || r.Header.Get("Authorization") != "Bearer oc-key" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rolling": map[string]any{
				"used_percent": 12.5,
				"resets_at":    "2026-10-28T10:00:00Z",
			},
			"weekly": map[string]any{
				"used":  20,
				"limit": 100,
			},
			"monthly": map[string]any{
				"status": "rate-limited", "percent": 100,
				"resetsAt": "2026-11-09T09:39:43.000Z",
			},
			"models": map[string]any{
				"claude-sonnet-5": map[string]any{"usagePercent": 40, "resetsAt": 1790000000},
				"gpt-5":           map[string]any{"used": 50, "limit": 100},
			},
		})
	}))
	defer srv.Close()

	p := New(Profile{ID: "opencode", QuotaKind: "opencode_go", DefaultBaseURL: "https://opencode.ai/zen/v1"})
	env := quotaEnvFor(srv, "opencode", map[string]string{"base_url": srv.URL, "api_key": "oc-key"})
	q, err := p.QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Plan != "Go" || q.ReadAt == 0 {
		t.Fatalf("q = %+v", q)
	}
	byName := map[string]provider.QuotaWindow{}
	for _, w := range q.Windows {
		byName[w.Name] = w
	}
	if w, ok := byName["5 hours"]; !ok || w.Used != 12.5 || w.ResetsAt == 0 {
		t.Fatalf("rolling = %+v", q.Windows)
	}
	if w, ok := byName["7 days"]; !ok || w.Used != 20 {
		t.Fatalf("weekly = %+v", q.Windows)
	}
	if w, ok := byName["claude-sonnet-5"]; !ok || w.Family != "Claude" || w.Pool != "Models" || w.Used != 40 {
		t.Fatalf("model window = %+v", q.Windows)
	}
	if w, ok := byName["gpt-5"]; !ok || w.Used != 50 {
		t.Fatalf("used/limit window = %+v", q.Windows)
	}
	if w, ok := byName["30 days"]; !ok || w.Used != 100 || w.ResetsAt == 0 {
		t.Fatalf("monthly rate-limited 窗口应展示为已用尽: %+v", q.Windows)
	}
}

func TestOpenCodeGoUsageNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := New(Profile{ID: "opencode", QuotaKind: "opencode_go"})
	env := quotaEnvFor(srv, "opencode", map[string]string{"base_url": srv.URL, "api_key": "nope"})
	if _, err := p.QueryQuota(context.Background(), env); err == nil || !strings.Contains(err.Error(), "OpenCode Go") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenCodeGoUsageUnknownShape(t *testing.T) {
	if _, err := parseOpenCodeUsage([]byte(`{"hello":"world"}`)); err == nil {
		t.Fatal("认不出窗口时应当报错")
	}
}

func TestMapStatusPaymentRequired(t *testing.T) {
	ae := mapStatusCode(402, `{"error":"Insufficient account funds"}`, nil)
	if ae == nil || !ae.Retryable || ae.StatusCode() != 402 {
		t.Fatalf("402 映射 = %+v", ae)
	}
	if !strings.Contains(ae.Message, "余额不足") {
		t.Fatalf("message = %q", ae.Message)
	}
}

func TestCodexWhamQuota(t *testing.T) {
	var seenAccount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/wham/usage" {
			http.NotFound(w, r)
			return
		}
		seenAccount = r.Header.Get("chatgpt-account-id")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan_type": "plus",
			"rate_limit": map[string]any{
				"primary_window":   map[string]any{"used_percent": 12, "limit_window_seconds": 18000, "reset_after_seconds": 600},
				"secondary_window": map[string]any{"used_percent": 40, "limit_window_seconds": 604800, "reset_at": 1790000000},
			},
			"credits":                  map[string]any{"has_credits": true, "balance": "12.5"},
			"rate_limit_reset_credits": map[string]any{"available_count": 2},
		})
	}))
	defer srv.Close()

	p := New(Profile{ID: "codex", QuotaKind: "codex"})
	env := quotaEnvFor(srv, "codex", map[string]string{
		"base_url": srv.URL + "/backend-api/codex", "access_token": "tok", "account_id": "acc-1",
	})
	q, err := p.QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if seenAccount != "acc-1" || q.Plan != "plus" || len(q.Windows) != 2 {
		t.Fatalf("q = %+v account = %q", q, seenAccount)
	}
	if q.Windows[0].Name != "5 hours" || q.Windows[0].Used != 12 || q.Windows[0].ResetsAt == 0 {
		t.Fatalf("primary = %+v", q.Windows[0])
	}
	if q.Windows[1].Name != "7 days" || q.Windows[1].Used != 40 || q.Windows[1].ResetsAt != 1790000000 {
		t.Fatalf("secondary = %+v", q.Windows[1])
	}
	if q.Balance != 12.5 || q.BalanceUnit != "credits" {
		t.Fatalf("credits = %+v", q)
	}
	if q.Resets == nil || q.Resets.Count != 2 {
		t.Fatalf("resets = %+v", q.Resets)
	}
}

func TestCodexWithoutAccountFallsBackToRelay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/usage" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"remaining":3.25}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := New(Profile{ID: "codex", QuotaKind: "codex"})
	env := quotaEnvFor(srv, "codex", map[string]string{"base_url": srv.URL, "access_token": "tok"})
	q, err := p.QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Balance != 3.25 || q.BalanceUnit != "USD" {
		t.Fatalf("fallback q = %+v", q)
	}
}

func TestCodexWhamEmptyReply(t *testing.T) {
	if _, err := parseCodexWham([]byte(`{"plan_type":"plus"}`)); err == nil {
		t.Fatal("没有窗口/积分时应报错")
	}
}

func TestCodexWindowName(t *testing.T) {
	if codexWindowName(18000) != "5 hours" || codexWindowName(604800) != "7 days" || codexWindowName(30*86400) != "30 days" || codexWindowName(0) != "" {
		t.Fatal("窗口名映射错误")
	}
}

func TestCodexAuthFormHasAccountID(t *testing.T) {
	form := New(Profile{ID: "codex", QuotaKind: "codex", Auths: []provider.AuthMethod{provider.AuthManualInput}}).Manifest().AuthFormSchema
	for _, f := range form {
		if f.Key == "account_id" {
			return
		}
	}
	t.Fatalf("Codex 表单缺少 account_id: %+v", form)
}

// 带 account_id 的网关账号：wham 不通时退回面板账单。
func TestCodexWhamFallsBackToRelay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/usage":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"remaining":3.25}`))
		default:
			http.NotFound(w, r) // /wham/usage 不存在
		}
	}))
	defer srv.Close()
	p := New(Profile{ID: "codex", QuotaKind: "codex"})
	env := quotaEnvFor(srv, "codex", map[string]string{
		"base_url": srv.URL, "access_token": "tok", "account_id": "acc-1",
	})
	q, err := p.QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Balance != 3.25 || q.BalanceUnit != "USD" {
		t.Fatalf("q = %+v", q)
	}
}
