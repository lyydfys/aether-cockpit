package qoder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestQueryQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me/usage" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer qoder-test-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"expires_at": 1796140800000,
			"plan_tier": "pro",
			"plan_tier_name": "Pro",
			"is_highest_tier": false,
			"user_quota": {"unit": "credits", "total": 2000, "used": 567, "remaining": 1433},
			"add_on_quota": {"unit": "credits", "total": 181, "used": 181, "remaining": 0}
		}`))
	}))
	defer srv.Close()

	p := newProvider(cnProfile)
	p.profile.openAPI = srv.URL
	env := provider.NewAccountEnv("qoder-quota-test", "qoder-cn")
	env.Credentials = map[string]string{"token": "qoder-test-token", "user_id": "test-user", "machine_id": "test-machine"}
	env.HTTPClient = srv.Client()

	q, err := p.QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if q.Plan != "Pro" || q.Total != 2000 || q.Used != 567 || q.Remaining != 1433 {
		t.Fatalf("quota = %+v", q)
	}
	if q.Balance != 1433 || q.BalanceUnit != "credits" || q.Until != 1796140800 {
		t.Fatalf("balance/until = %+v", q)
	}
	if len(q.Windows) != 2 {
		t.Fatalf("windows = %+v", q.Windows)
	}
	if w := q.Windows[0]; w.Name != "Pro 额度" || w.Limit != 2000 || w.Amount != 567 ||
		w.Used < 28.3 || w.Used > 28.4 || w.Unit != "credits" || w.ResetsAt != 1796140800 {
		t.Fatalf("plan window = %+v", w)
	}
	if w := q.Windows[1]; w.Name != "加量包" || w.Limit != 181 || w.Used != 100 || !w.Aside {
		t.Fatalf("add-on window = %+v", w)
	}
}

func TestQueryQuotaAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := newProvider(intlProfile)
	p.profile.openAPI = srv.URL
	env := provider.NewAccountEnv("qoder-quota-401", "qoder-intl")
	env.Credentials = map[string]string{"token": "bad", "user_id": "test-user", "machine_id": "test-machine"}
	env.HTTPClient = srv.Client()

	_, err := p.QueryQuota(context.Background(), env)
	if err == nil || !strings.Contains(err.Error(), "鉴权失败") {
		t.Fatalf("err = %v", err)
	}
}
