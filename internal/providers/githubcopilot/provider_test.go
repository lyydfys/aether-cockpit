package githubcopilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestParseCopilotHosts(t *testing.T) {
	raw := []byte(`{"github.com":{"user":"octocat","oauth_token":"ghu_test_token"}}`)
	got := parseCopilotHosts(raw)
	if len(got) != 1 {
		t.Fatalf("解析条目 = %d, want 1", len(got))
	}
	d := got[0]
	if d.Credentials["api_key"] != "ghu_test_token" || d.Credentials["github_token"] != "ghu_test_token" {
		t.Fatalf("凭证不对: %+v", d.Credentials)
	}
	if d.Label == "" || d.Credentials["base_url"] != "https://api.githubcopilot.com" {
		t.Fatalf("标签/base_url 不对: %+v", d)
	}
	if len(parseCopilotHosts([]byte(`not json`))) != 0 {
		t.Fatal("非法 JSON 应返回 nil")
	}
}

func TestManifestDeviceCode(t *testing.T) {
	m := (&Provider{}).Manifest()
	if m.OAuth == nil || m.OAuth.DeviceCodeURL == "" || m.OAuth.ClientID == "" {
		t.Fatalf("缺少设备码声明: %+v", m.OAuth)
	}
	found := false
	for _, a := range m.SupportedAuths {
		if a == provider.AuthDeviceCode {
			found = true
		}
	}
	if !found || !m.Capabilities.Quota || !m.Capabilities.Chat {
		t.Fatalf("能力声明不对: auths=%v caps=%+v", m.SupportedAuths, m.Capabilities)
	}
	if _, ok := interface{}(&Provider{}).(provider.QuotaCapability); !ok {
		t.Fatal("未实现 QuotaCapability")
	}
}

func TestQueryQuotaMapsSnapshots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ghu_test_token" {
			t.Errorf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"copilot_plan":     "individual",
			"chat_enabled":     true,
			"quota_reset_date": "2026-11-01",
			"quota_snapshots": map[string]any{
				"premium_interactions": map[string]any{"entitlement": 300, "remaining": 120, "percent_remaining": 40},
				"chat":                 map[string]any{"entitlement": 0, "remaining": 0, "percent_remaining": 0, "unlimited": true},
				"completions":          map[string]any{"entitlement": 2000, "remaining": 1500, "percent_remaining": 75},
			},
		})
	}))
	defer srv.Close()
	old := copilotUserURL
	copilotUserURL = srv.URL
	defer func() { copilotUserURL = old }()

	env := provider.NewAccountEnv("copilot-test", channelID)
	env.Credentials = map[string]string{"github_token": "ghu_test_token"}
	env.HTTPClient = &http.Client{}
	q, err := (&Provider{}).QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Plan != "individual" || q.Total != 300 || q.Remaining != 120 || q.Used != 180 {
		t.Fatalf("总额度不对: %+v", q)
	}
	if len(q.Windows) != 2 {
		t.Fatalf("窗口数 = %d, want 2 (unlimited chat 跳过): %+v", len(q.Windows), q.Windows)
	}
	first := q.Windows[0]
	if first.Name != "Premium requests" || first.Limit != 300 || first.Amount != 180 || first.Used != 60 || first.ResetsAt == 0 {
		t.Fatalf("premium 窗口不对: %+v", first)
	}
	if q.Windows[1].Name != "Completions" || q.Windows[1].Used != 25 {
		t.Fatalf("completions 窗口不对: %+v", q.Windows[1])
	}
}

func TestQueryQuotaAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer srv.Close()
	old := copilotUserURL
	copilotUserURL = srv.URL
	defer func() { copilotUserURL = old }()

	env := provider.NewAccountEnv("copilot-test", channelID)
	env.Credentials = map[string]string{"api_key": "bad"}
	env.HTTPClient = &http.Client{}
	if _, err := (&Provider{}).QueryQuota(context.Background(), env); err == nil {
		t.Fatal("401 应返回错误")
	}
}
