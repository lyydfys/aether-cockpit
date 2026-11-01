package claude

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func TestQueryQuotaParsesUsageWindows(t *testing.T) {
	var gotAuth, gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != claudeUsagePath {
			t.Errorf("path = %q, want %q", r.URL.Path, claudeUsagePath)
		}
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"five_hour": {"utilization": 42.5, "resets_at": "2026-10-27T12:00:00Z"},
			"seven_day": {"utilization": 7, "resets_at": 1793102400},
			"seven_day_opus": null
		}`))
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("a1", channelID)
	env.SetCredential("access_token", "sk-ant-oat01-test")
	env.SetCredential("base_url", srv.URL)
	env.HTTPClient = srv.Client()

	p := &Provider{}
	q, err := p.QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if gotAuth != "Bearer sk-ant-oat01-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotBeta != oauthBetaHeader {
		t.Errorf("anthropic-beta = %q", gotBeta)
	}
	if len(q.Windows) != 2 {
		t.Fatalf("windows = %+v, want 2", q.Windows)
	}
	if q.Windows[0].Name != "5 hours" || q.Windows[0].Used != 42.5 {
		t.Errorf("5h 窗口 = %+v", q.Windows[0])
	}
	if q.Windows[0].ResetsAt != time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("5h resetsAt = %d", q.Windows[0].ResetsAt)
	}
	if q.Windows[1].Name != "Weekly" || q.Windows[1].ResetsAt != 1793102400 {
		t.Errorf("weekly 窗口 = %+v", q.Windows[1])
	}
	if q.ReadAt == 0 {
		t.Error("ReadAt 未填写")
	}
}

func TestQueryQuotaRejectsEmptyPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"five_hour": null, "seven_day": null}`))
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("a1", channelID)
	env.SetCredential("access_token", "tok")
	env.SetCredential("base_url", srv.URL)
	env.HTTPClient = srv.Client()

	if _, err := (&Provider{}).QueryQuota(context.Background(), env); err == nil {
		t.Fatal("没有窗口数据时应报错，而不是返回空配额")
	}
}
