package kiro

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestQueryQuotaUsageBreakdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/getUsageLimits" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("profileArn") != "arn:aws:codewhisperer:us-east-1:1:profile/x" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nextDateReset": 1767225600,
			"subscriptionInfo": map[string]any{
				"subscriptionTitle": "KIRO PRO",
			},
			"usageBreakdownList": []any{
				map[string]any{
					"resourceType":              "AGENTIC_REQUEST",
					"currentUsage":              250,
					"currentUsageWithPrecision": 250.0,
					"usageLimit":                1000,
					"usageLimitWithPrecision":   1000.0,
				},
				map[string]any{
					"resourceType": "CODE_COMPLETIONS",
					"currentUsage": 100,
					"usageLimit":   100,
				},
			},
		})
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("k1", "kiro")
	env.Credentials = map[string]string{
		"access_token": "tok",
		"profile_arn":  "arn:aws:codewhisperer:us-east-1:1:profile/x",
		"base_url":     srv.URL,
	}
	env.HTTPClient = srv.Client()

	q, err := (&Provider{}).QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Plan != "KIRO PRO" || q.BalanceUnit != "credits" || q.Balance != 750 {
		t.Fatalf("q = %+v", q)
	}
	if len(q.Windows) != 2 {
		t.Fatalf("windows = %+v", q.Windows)
	}
	if q.Windows[0].Name != "Agentic requests" || q.Windows[0].Used != 25 || q.Windows[0].ResetsAt != 1767225600 {
		t.Fatalf("window[0] = %+v", q.Windows[0])
	}
	if q.Windows[1].Name != "Code completions" || q.Windows[1].Used != 100 {
		t.Fatalf("window[1] = %+v", q.Windows[1])
	}
}

func TestQueryQuotaMissingProfileArn(t *testing.T) {
	env := provider.NewAccountEnv("k2", "kiro")
	env.Credentials = map[string]string{"access_token": "tok"}
	if _, err := (&Provider{}).QueryQuota(context.Background(), env); err == nil {
		t.Fatal("缺少 profile_arn 时应报错")
	}
}
