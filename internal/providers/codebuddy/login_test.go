package codebuddy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func TestLoginFlow(t *testing.T) {
	tokenCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v2/plugin/auth/state"):
			if r.URL.Query().Get("platform") != "ide" {
				t.Errorf("platform = %q", r.URL.Query().Get("platform"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "cb-st"}})
		case strings.HasPrefix(r.URL.Path, "/v2/plugin/auth/token"):
			tokenCalls++
			if tokenCalls < 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 1})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"accessToken": "cb-token", "refreshToken": "cb-rt", "domain": "d.example",
			}})
		case strings.HasPrefix(r.URL.Path, "/v2/plugin/login/account"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"uid": "u-cb", "nickname": "Nick", "email": "cb@example.com", "enterpriseId": "ent-cb",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := cbAPIBaseOverride
	cbAPIBaseOverride = srv.URL
	defer func() { cbAPIBaseOverride = old }()

	s, err := StartLogin("codebuddy-intl")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	u, _ := url.Parse(s.LoginURL)
	if u.Query().Get("state") != "cb-st" || u.Query().Get("loginSessionId") == "" {
		t.Fatalf("login url = %s", s.LoginURL)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if GetLogin(s.ID).State == "done" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	got := GetLogin(s.ID)
	if got.State != "done" {
		t.Fatalf("state = %s err=%s", got.State, got.Err)
	}
	creds := Credentials(s.ID)
	for k, want := range map[string]string{
		"token": "cb-token", "refresh_token": "cb-rt", "user_id": "u-cb",
		"user_email": "cb@example.com", "enterprise_id": "ent-cb", "base_url": srv.URL,
	} {
		if creds[k] != want {
			t.Fatalf("%s = %q, want %q (%+v)", k, creds[k], want, creds)
		}
	}
	if !MarkCommitted(s.ID, "acct-1") || MarkCommitted(s.ID, "acct-2") {
		t.Fatal("提交标记应只成功一次")
	}
}

func TestManifestAndValidation(t *testing.T) {
	for _, id := range []string{"codebuddy-intl", "codebuddy-cn"} {
		p, err := Validate(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		m := p.Manifest()
		if m.Family != "codebuddy" || !m.Capabilities.Chat {
			t.Fatalf("%s manifest: %+v", id, m)
		}
		if _, ok := provider.Get(id); !ok {
			t.Fatalf("%s 未注册", id)
		}
	}
	if _, err := StartLogin("codex"); err == nil {
		t.Fatal("非 CodeBuddy 渠道应报错")
	}
}
