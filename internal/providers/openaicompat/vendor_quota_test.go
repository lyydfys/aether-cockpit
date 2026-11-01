package openaicompat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestVendorQuotaProbes(t *testing.T) {
	cases := []struct {
		name     string
		handler  http.HandlerFunc
		path     string
		probe    func(context.Context, *provider.AccountEnv, string, string) (*provider.QuotaStatus, error)
		wantBal  float64
		wantUnit string
	}{
		{
			name: "deepseek",
			path: "/user/balance",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer k" {
					t.Errorf("auth = %q", r.Header.Get("Authorization"))
				}
				_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"12.50","granted_balance":"2.50","topped_up_balance":"10.00"}]}`))
			},
			probe:    queryDeepSeekQuota,
			wantBal:  12.5,
			wantUnit: "CNY",
		},
		{
			name: "moonshot",
			path: "/v1/users/me/balance",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"code":0,"status":true,"data":{"available_balance":49.58,"voucher_balance":1.5,"cash_balance":48.08}}`))
			},
			probe:    queryMoonshotQuota,
			wantBal:  49.58,
			wantUnit: "USD", // 假上游不是 moonshot.cn，按国际站计
		},
		{
			name: "siliconflow",
			path: "/v1/user/info",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"code":20000,"message":"OK","status":true,"data":{"balance":"0.88","chargeBalance":"88.00","totalBalance":"88.88"}}`))
			},
			probe:    querySiliconFlowQuota,
			wantBal:  88.88,
			wantUnit: "CNY",
		},
		{
			name: "openrouter",
			path: "/api/v1/credits",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"data":{"total_credits":100,"total_usage":84.14}}`))
			},
			probe:    queryOpenRouterQuota,
			wantBal:  15.86,
			wantUnit: "USD",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				c.handler(w, r)
			}))
			defer srv.Close()
			env := provider.NewAccountEnv("acc", c.name)
			env.Credentials = map[string]string{"base_url": srv.URL, "api_key": "k"}
			env.SetHTTPClient(srv.Client())
			q, err := c.probe(context.Background(), env, srv.URL, c.name)
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if gotPath != c.path {
				t.Errorf("path = %q, want %q", gotPath, c.path)
			}
			if q.Balance != c.wantBal || q.BalanceUnit != c.wantUnit {
				t.Errorf("balance = %v %q", q.Balance, q.BalanceUnit)
			}
			if q.Message == "" {
				t.Errorf("missing message")
			}
		})
	}
}
