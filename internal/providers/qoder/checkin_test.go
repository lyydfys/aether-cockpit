package qoder

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

func checkinEnv(t *testing.T, srv *httptest.Server) *provider.AccountEnv {
	t.Helper()
	env := provider.NewAccountEnv("acc-1", "qoder-test")
	env.Credentials = map[string]string{"token": "tk", "user_id": "uid-1", "machine_id": "mid-1"}
	env.HTTPClient = srv.Client()
	return env
}

func TestCheckinClaimsCampaign(t *testing.T) {
	var gotAuth, gotMachine string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMachine = r.Header.Get("Cosy-MachineId")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sash/api/v1/me/campaigns":
			_, _ = io.WriteString(w, `{"campaigns":[{"campaignId":"c1","actionType":"CLAIM_BENEFIT","claimStatus":"CLAIMABLE","benefit":{"amount":100}}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/sash/api/v1/me/campaigns/c1/claim":
			_, _ = io.WriteString(w, `{"status":"CLAIMED","benefit":{"amount":100}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := newProvider(profile{id: "qoder-test", openAPI: srv.URL})
	report, err := p.ExecuteTasks(context.Background(), checkinEnv(t, srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tk" || gotMachine != "mid-1" {
		t.Fatalf("auth=%q machine=%q", gotAuth, gotMachine)
	}
	if report.Outcome != provider.CheckinClaimed || report.Credit != 100 {
		t.Fatalf("report = %+v", report)
	}
}

func TestCheckinInactiveAndDone(t *testing.T) {
	cases := []struct {
		body    string
		outcome string
	}{
		{`{"campaigns":[]}`, provider.CheckinInactive},
		{`{"campaigns":[{"campaignId":"c1","actionType":"CLAIM_BENEFIT","claimStatus":"CLAIMED","startAt":1,"endAt":99999999999,"benefit":{"amount":30}}]}`, provider.CheckinDone},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, tc.body)
		}))
		p := newProvider(profile{id: "qoder-test", openAPI: srv.URL})
		report, _ := p.ExecuteTasks(context.Background(), checkinEnv(t, srv), nil)
		srv.Close()
		if report.Outcome != tc.outcome {
			t.Fatalf("%s → %q, want %q", tc.body, report.Outcome, tc.outcome)
		}
	}
}

func TestCheckinTasksBothSites(t *testing.T) {
	for _, id := range []string{"qoder-cn", "qoder-intl"} {
		p, ok := provider.Get(id)
		if !ok {
			t.Fatalf("%s 未注册", id)
		}
		if len(p.Manifest().Tasks) == 0 || !p.Manifest().Capabilities.CheckIn {
			t.Fatalf("%s 应声明签到任务", id)
		}
	}
}
