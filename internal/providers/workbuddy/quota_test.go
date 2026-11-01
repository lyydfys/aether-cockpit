package workbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func wbQuotaEnv(srv *httptest.Server) *provider.AccountEnv {
	env := provider.NewAccountEnv("wb-quota", "workbuddy-cn")
	env.Credentials = map[string]string{
		"token": "tok", "user_id": "u1", "base_url": srv.URL, "domain": "www.workbuddy.cn",
	}
	env.HTTPClient = srv.Client()
	return env
}

func TestWorkBuddyQuotaPackages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/get-user-resource-summary") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"IsPaidUser": true,
			"Packages": []any{
				map[string]any{"CycleTotalCapacity": "1000", "CycleUsedCapacity": 250},
			},
		})
	}))
	defer srv.Close()

	q, err := newProvider(cnProfile).QueryQuota(context.Background(), wbQuotaEnv(srv))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Plan != "Pro" || q.BalanceUnit != "credits" || q.Balance != 750 {
		t.Fatalf("q = %+v", q)
	}
	if len(q.Windows) != 1 {
		t.Fatalf("windows = %+v", q.Windows)
	}
	w := q.Windows[0]
	if w.Name != "Credits" || w.Limit != 1000 || w.Amount != 250 || w.Used < 24.9 || w.Used > 25.1 || w.Unit != "credits" {
		t.Fatalf("window = %+v", w)
	}
}

func TestWorkBuddyQuotaEnterprise(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/get-user-resource-summary"):
			_ = json.NewEncoder(w).Encode(map[string]any{"IsPaidUser": true, "Packages": []any{}})
		case strings.HasSuffix(r.URL.Path, "/get-enterprise-user-usage"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"limitNum": "100", "credit": 30,
				"cycleStartTime": "2026-10-21 00:00:00", "cycleResetTime": "2026-10-28 00:00:00",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	q, err := newProvider(cnProfile).QueryQuota(context.Background(), wbQuotaEnv(srv))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Balance != 70 || len(q.Windows) != 1 || q.Windows[0].Used < 29.9 || q.Windows[0].Used > 30.1 {
		t.Fatalf("q = %+v", q)
	}
	if q.Windows[0].ResetsAt == 0 {
		t.Fatal("cycleResetTime 未解析")
	}
}

func TestWorkBuddyQuotaFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := newProvider(cnProfile).QueryQuota(context.Background(), wbQuotaEnv(srv)); err == nil {
		t.Fatal("401 应返回错误")
	}
}
