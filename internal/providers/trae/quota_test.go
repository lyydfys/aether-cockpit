package trae

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestTraeQuotaCredits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/status") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "enable": true, "checked_in": true,
			"credits": 42, "extra_credits": 8,
		})
	}))
	defer srv.Close()

	old := traeCheckinURL
	traeCheckinURL = srv.URL + "/trae/api/v2/ug/checkin_credits"
	t.Cleanup(func() { traeCheckinURL = old })

	env := provider.NewAccountEnv("trae-quota", "trae-cn")
	env.Credentials = map[string]string{"token": "tok"}
	env.HTTPClient = srv.Client()

	q, err := newProvider("trae-cn", "CN").QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Balance != 50 || q.BalanceUnit != "credits" {
		t.Fatalf("q = %+v", q)
	}
}

func TestTraeIntlNoQuota(t *testing.T) {
	env := provider.NewAccountEnv("trae-intl", "trae-intl")
	env.Credentials = map[string]string{"token": "tok"}
	if _, err := newProvider("trae-intl", "SG").QueryQuota(context.Background(), env); err == nil {
		t.Fatal("国际版应如实报错")
	}
}
