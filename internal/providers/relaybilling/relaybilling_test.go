package relaybilling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func relayEnv(srv *httptest.Server, base string) *provider.AccountEnv {
	env := provider.NewAccountEnv("relay-1", "codex")
	env.Credentials = map[string]string{"base_url": base, "api_key": "sk-relay"}
	env.HTTPClient = srv.Client()
	return env
}

func TestTokenUsageFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-relay" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/usage/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "token_usage", "total_available": 250000, "total_granted": 500000, "total_used": 250000,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	q, err := Query(context.Background(), relayEnv(srv, srv.URL+"/v1"), srv.URL+"/v1")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if q.Balance != 0.5 || q.BalanceUnit != "USD" || !strings.Contains(q.Message, "Key 额度") {
		t.Fatalf("q = %+v", q)
	}
}

func TestCreditGrantsFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dashboard/billing/credit_grants" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"total_available": 12.5, "total_granted": 20, "total_used": 7.5})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	q, err := Query(context.Background(), relayEnv(srv, srv.URL), srv.URL)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if q.Balance != 12.5 || !strings.Contains(q.Message, "面板信贷") {
		t.Fatalf("q = %+v", q)
	}
}

func TestNoBillingAnywhere(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if _, err := Query(context.Background(), relayEnv(srv, srv.URL), srv.URL); err == nil || !strings.Contains(err.Error(), "账单接口") {
		t.Fatalf("err = %v", err)
	}
}

func TestV1UsageRemainingFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/usage" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"remaining": 3.25})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	q, err := Query(context.Background(), relayEnv(srv, srv.URL), srv.URL)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if q.Balance != 3.25 || q.BalanceUnit != "USD" || !strings.Contains(q.Message, "剩余 $3.25") {
		t.Fatalf("q = %+v", q)
	}
}
