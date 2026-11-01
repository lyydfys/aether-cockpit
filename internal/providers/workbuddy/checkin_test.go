package workbuddy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func checkinEnv(t *testing.T, srv *httptest.Server) *provider.AccountEnv {
	t.Helper()
	env := provider.NewAccountEnv("acc-1", "workbuddy-cn")
	env.Credentials = map[string]string{
		"token":    "tk",
		"user_id":  "uid-1",
		"base_url": srv.URL + "/v2",
	}
	env.HTTPClient = srv.Client()
	return env
}

func TestCheckinClaimed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tk" || r.Header.Get("X-User-Id") != "uid-1" {
			t.Errorf("headers = %q %q", r.Header.Get("Authorization"), r.Header.Get("X-User-Id"))
		}
		switch r.URL.Path {
		case "/v2/billing/meter/checkin-activity-status":
			_, _ = io.WriteString(w, `{"active":true,"today_checked_in":false,"streak_days":2}`)
		case "/v2/billing/meter/daily-checkin":
			_, _ = io.WriteString(w, `{"credit":"100","streak_days":"3","message":"签到成功"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := &Provider{profile: cnProfile}
	report, err := p.ExecuteTasks(context.Background(), checkinEnv(t, srv), []string{"daily_checkin"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != provider.CheckinClaimed || report.Credit != 100 || report.Streak != 3 {
		t.Fatalf("report = %+v", report)
	}
	if report.Day != provider.CheckinDay(time.Now()) {
		t.Fatalf("day = %q", report.Day)
	}
	if !report.Success || len(report.Results) != 1 || !report.Results[0].Success {
		t.Fatalf("results = %+v", report)
	}
}

func TestCheckinDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"active":true,"today_checked_in":true,"today_credit":50,"streak_days":4}`)
	}))
	defer srv.Close()

	p := &Provider{profile: cnProfile}
	report, err := p.ExecuteTasks(context.Background(), checkinEnv(t, srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != provider.CheckinDone || report.Credit != 50 || report.Streak != 4 {
		t.Fatalf("report = %+v", report)
	}
}

func TestCheckinInactive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"active":false}`)
	}))
	defer srv.Close()

	report, err := (&Provider{profile: cnProfile}).ExecuteTasks(context.Background(), checkinEnv(t, srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Outcome != provider.CheckinInactive || report.Success || !report.Results[0].Skipped {
		t.Fatalf("report = %+v", report)
	}
}

func TestCheckinRefusedCodes(t *testing.T) {
	cases := []struct {
		body    string
		outcome string
	}{
		{`{"code":1001,"msg":"already"}`, provider.CheckinDone},
		{`{"code":1002,"msg":"ineligible"}`, provider.CheckinIneligible},
		{`{"code":1003,"msg":"ended"}`, provider.CheckinInactive},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, tc.body)
		}))
		report, err := (&Provider{profile: cnProfile}).ExecuteTasks(context.Background(), checkinEnv(t, srv), nil)
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if report.Outcome != tc.outcome {
			t.Fatalf("%s → %q, want %q", tc.body, report.Outcome, tc.outcome)
		}
	}
}

func TestCheckinTasksOnlyCN(t *testing.T) {
	cn, _ := provider.Get("workbuddy-cn")
	if len(cn.Manifest().Tasks) == 0 || !cn.Manifest().Capabilities.CheckIn {
		t.Fatal("workbuddy-cn 应声明签到任务")
	}
	intl, _ := provider.Get("workbuddy-intl")
	if len(intl.Manifest().Tasks) != 0 || intl.Manifest().Capabilities.CheckIn {
		t.Fatal("workbuddy-intl 不应有签到任务")
	}
}
