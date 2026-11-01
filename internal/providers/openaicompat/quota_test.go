package openaicompat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestQueryQuotaRelayBilling(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/dashboard/billing/subscription":
			_ = json.NewEncoder(w).Encode(map[string]any{"hard_limit_usd": 10})
		case "/dashboard/billing/usage":
			_ = json.NewEncoder(w).Encode(map[string]any{"total_usage": 250})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := &Provider{}
	q, err := p.QueryQuota(context.Background(), newEnv(srv.URL+"/v1"))
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.BalanceUnit != "USD" || q.Balance < 7.49 || q.Balance > 7.51 {
		t.Fatalf("q = %+v", q)
	}

	var _ provider.QuotaCapability = p
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
