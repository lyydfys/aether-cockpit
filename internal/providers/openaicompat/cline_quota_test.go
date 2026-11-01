package openaicompat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestClineQuotaBalanceAndWindows(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-cline" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/v1/users/me":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"user-1","email":"a@b.c"}}`))
		case "/api/v1/users/user-1/balance":
			_, _ = w.Write([]byte(`{"success":true,"data":{"balance":1234500,"userId":"user-1"}}`))
		case "/api/v1/users/me/plan/usage-limits":
			_, _ = w.Write([]byte(`{"success":true,"data":{"limits":[{"type":"five_hour","percentUsed":12.5,"resetsAt":"2026-10-28T03:00:00Z"},{"type":"weekly","percentUsed":40},{"type":"monthly","percentUsed":90,"resetsAt":"2026-11-01T00:00:00Z"},{"type":"unknown","percentUsed":1}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc", "cline")
	env.Credentials = map[string]string{"base_url": srv.URL + "/api/v1", "api_key": "sk-cline"}
	env.SetHTTPClient(srv.Client())
	q, err := queryClineQuota(context.Background(), env, "https://api.cline.bot/api/v1", "cline")
	if err != nil {
		t.Fatalf("queryClineQuota: %v", err)
	}
	if q.Balance != 1.2345 || q.BalanceUnit != "USD" {
		t.Errorf("balance = %v %q", q.Balance, q.BalanceUnit)
	}
	if len(q.Windows) != 3 {
		t.Fatalf("windows = %+v", q.Windows)
	}
	if q.Windows[0].Name != "5 小时" || q.Windows[0].Used != 12.5 || q.Windows[0].ResetsAt == 0 {
		t.Errorf("first window = %+v", q.Windows[0])
	}
	if q.Windows[1].Name != "7 天" || q.Windows[1].Used != 40 || q.Windows[1].ResetsAt != 0 {
		t.Errorf("second window = %+v", q.Windows[1])
	}
	if q.Windows[2].Name != "30 天" || q.Windows[2].Used != 90 {
		t.Errorf("third window = %+v", q.Windows[2])
	}
}

func TestClineQuotaWithoutPlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/users/me":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"u"}}`))
		case "/api/v1/users/u/balance":
			_, _ = w.Write([]byte(`{"balance":500000}`))
		case "/api/v1/users/me/plan/usage-limits":
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc", "cline")
	env.Credentials = map[string]string{"base_url": srv.URL + "/api/v1", "api_key": "k"}
	env.SetHTTPClient(srv.Client())
	q, err := queryClineQuota(context.Background(), env, srv.URL+"/api/v1", "cline")
	if err != nil {
		t.Fatalf("queryClineQuota: %v", err)
	}
	if q.Balance != 0.5 || len(q.Windows) != 0 {
		t.Errorf("q = %+v", q)
	}
}
