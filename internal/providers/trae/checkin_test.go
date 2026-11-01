package trae

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
	env := provider.NewAccountEnv("acc-1", "trae-cn")
	env.Credentials = map[string]string{"token": "tk", "device_id": "dev-1", "machine_id": "mch"}
	env.HTTPClient = srv.Client()
	return env
}

func withCheckinURL(t *testing.T, url string) {
	t.Helper()
	old := traeCheckinURL
	traeCheckinURL = url
	t.Cleanup(func() { traeCheckinURL = old })
}

func TestCheckinClaim(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/status":
			_, _ = io.WriteString(w, `{"code":0,"enable":true,"checked_in":false,"credits":100}`)
		case "/claim":
			_, _ = io.WriteString(w, `{"code":0,"credits":100,"extra_credits":5}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	withCheckinURL(t, srv.URL)

	report, err := (&Provider{id: "trae-cn", region: "CN"}).ExecuteTasks(context.Background(), checkinEnv(t, srv), nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Cloud-IDE-JWT tk" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if report.Outcome != provider.CheckinClaimed || report.Credit != 105 {
		t.Fatalf("report = %+v", report)
	}
}

func TestCheckinAlreadyDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":0,"enable":true,"checked_in":true,"credits":80}`)
	}))
	defer srv.Close()
	withCheckinURL(t, srv.URL)

	report, _ := (&Provider{id: "trae-cn", region: "CN"}).ExecuteTasks(context.Background(), checkinEnv(t, srv), nil)
	if report.Outcome != provider.CheckinDone || report.Credit != 80 {
		t.Fatalf("report = %+v", report)
	}
}

func TestCheckinRefused(t *testing.T) {
	cases := []struct {
		body    string
		outcome string
	}{
		{`{"code":9095,"message":"device in"}`, provider.CheckinIneligible},
		{`{"code":1001,"message":"signed out"}`, provider.CheckinFailed},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, tc.body)
		}))
		withCheckinURL(t, srv.URL)
		report, _ := (&Provider{id: "trae-cn", region: "CN"}).ExecuteTasks(context.Background(), checkinEnv(t, srv), nil)
		srv.Close()
		if report.Outcome != tc.outcome {
			t.Fatalf("%s → %q, want %q", tc.body, report.Outcome, tc.outcome)
		}
	}
}

func TestCheckinClaimOwnApp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			_, _ = io.WriteString(w, `{"code":0,"enable":true,"checked_in":false}`)
			return
		}
		_, _ = io.WriteString(w, `{"code":9074,"message":"too many"}`)
	}))
	defer srv.Close()
	withCheckinURL(t, srv.URL)

	report, _ := (&Provider{id: "trae-cn", region: "CN"}).ExecuteTasks(context.Background(), checkinEnv(t, srv), nil)
	if report.Outcome != provider.CheckinOwnApp {
		t.Fatalf("report = %+v", report)
	}
}

func TestCheckinTasksOnlyCN(t *testing.T) {
	cn, _ := provider.Get("trae-cn")
	if len(cn.Manifest().Tasks) == 0 || !cn.Manifest().Capabilities.CheckIn {
		t.Fatal("trae-cn 应声明签到任务")
	}
	intl, _ := provider.Get("trae-intl")
	if len(intl.Manifest().Tasks) != 0 || intl.Manifest().Capabilities.CheckIn {
		t.Fatal("trae-intl 不应有签到任务")
	}
}
