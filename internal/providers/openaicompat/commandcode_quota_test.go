package openaicompat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestCommandCodeQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cc-key" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/alpha/whoami":
			_, _ = w.Write([]byte(`{"org":{"id":"org-1"}}`))
		case "/alpha/billing/credits":
			if r.URL.Query().Get("orgId") != "org-1" {
				t.Errorf("orgId = %q", r.URL.Query().Get("orgId"))
			}
			_, _ = w.Write([]byte(`{"credits":{"monthlyCredits":5,"purchasedCredits":"1","freeCredits":0.5},"windowLimits":{"limited":true,"fiveHour":{"used":50,"cap":100,"resetAt":1793154000000},"weekly":{"used":"20","cap":100,"resetAt":1793240400000}}}`))
		case "/alpha/billing/subscriptions":
			_, _ = w.Write([]byte(`{"data":{"planId":"individual-pro","currentPeriodStart":"2026-10-01T00:00:00Z","currentPeriodEnd":"2026-11-01T00:00:00Z"}}`))
		case "/alpha/usage/summary":
			if r.URL.Query().Get("since") != "2026-10-01T00:00:00Z" {
				t.Errorf("since = %q", r.URL.Query().Get("since"))
			}
			_, _ = w.Write([]byte(`{"totalMonthlyCredits":2.5,"totalCost":2.5}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc", "commandcode")
	env.Credentials = map[string]string{"base_url": srv.URL + "/provider/v1", "api_key": "cc-key"}
	env.SetHTTPClient(srv.Client())
	q, err := queryCommandCodeQuota(context.Background(), env, srv.URL+"/provider/v1", "commandcode")
	if err != nil {
		t.Fatalf("queryCommandCodeQuota: %v", err)
	}
	if q.Balance != 6.5 || q.BalanceUnit != "USD" {
		t.Errorf("balance = %v %q", q.Balance, q.BalanceUnit)
	}
	if q.Plan != "Pro" {
		t.Errorf("plan = %q", q.Plan)
	}
	if len(q.Windows) != 3 {
		t.Fatalf("windows = %+v", q.Windows)
	}
	if q.Windows[0].Name != "5 小时" || q.Windows[0].Used != 50 || q.Windows[0].ResetsAt == 0 {
		t.Errorf("5h window = %+v", q.Windows[0])
	}
	if q.Windows[1].Name != "7 天" || q.Windows[1].Used != 20 {
		t.Errorf("7d window = %+v", q.Windows[1])
	}
	if q.Windows[2].Name != "30 天" {
		t.Errorf("monthly window = %+v", q.Windows[2])
	}
	if got := q.Windows[2].Used; got < 33 || got > 34 {
		t.Errorf("monthly used = %v, want ~33.33", got)
	}
}

func TestCommandCodeQuotaCreditsOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alpha/whoami":
			_, _ = w.Write([]byte(`{}`))
		case "/alpha/billing/credits":
			_, _ = w.Write([]byte(`{"credits":{"monthlyCredits":0,"purchasedCredits":2,"freeCredits":0},"windowLimits":{"limited":false}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc", "commandcode")
	env.Credentials = map[string]string{"base_url": srv.URL + "/provider/v1", "api_key": "k"}
	env.SetHTTPClient(srv.Client())
	q, err := queryCommandCodeQuota(context.Background(), env, srv.URL+"/provider/v1", "commandcode")
	if err != nil {
		t.Fatalf("queryCommandCodeQuota: %v", err)
	}
	if q.Balance != 2 || len(q.Windows) != 0 {
		t.Errorf("q = %+v", q)
	}
}
