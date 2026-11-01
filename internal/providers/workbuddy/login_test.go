package workbuddy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLoginFlow(t *testing.T) {
	tokenCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v2/plugin/auth/state"):
			if r.Header.Get("X-No-Authorization") != "true" {
				t.Errorf("缺少 X-No-Authorization")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"state": "st-1"}})
		case strings.HasPrefix(r.URL.Path, "/v2/plugin/auth/token"):
			if r.URL.Query().Get("state") != "st-1" {
				t.Errorf("state = %q", r.URL.Query().Get("state"))
			}
			tokenCalls++
			if tokenCalls < 2 {
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "message": "pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"accessToken": "wb-token", "refreshToken": "wb-rt", "domain": "d.example",
			}})
		case strings.HasPrefix(r.URL.Path, "/v2/plugin/login/account"):
			if r.Header.Get("Authorization") != "Bearer wb-token" {
				t.Errorf("account Authorization = %q", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
				"uid": "u1", "nickname": "Nick", "email": "dev@example.com", "enterpriseId": "ent-1",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := wbAPIBase
	wbAPIBase = srv.URL
	defer func() { wbAPIBase = old }()

	s, err := StartLogin("workbuddy-cn")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	u, err := url.Parse(s.LoginURL)
	if err != nil {
		t.Fatalf("login url: %v", err)
	}
	if u.Query().Get("state") != "st-1" || u.Query().Get("loginSessionId") == "" {
		t.Fatalf("login url 参数不对: %s", s.LoginURL)
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
		"token": "wb-token", "refresh_token": "wb-rt", "user_id": "u1",
		"user_name": "Nick", "user_email": "dev@example.com", "domain": "d.example", "enterprise_id": "ent-1",
	} {
		if creds[k] != want {
			t.Fatalf("%s = %q, want %q (%+v)", k, creds[k], want, creds)
		}
	}
	if !MarkCommitted(s.ID, "acct-1") || MarkCommitted(s.ID, "acct-2") {
		t.Fatal("提交标记应只成功一次")
	}
}

func TestLoginRejectsOtherProvider(t *testing.T) {
	if _, err := StartLogin("qoder-cn"); err == nil {
		t.Fatal("非 WorkBuddy 渠道应报错")
	}
}
