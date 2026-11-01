package kiro

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func TestPortalLoginFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&map[string]any{})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accessToken": "kiro-access", "refreshToken": "kiro-refresh",
			"profileArn": "arn:aws:codewhisperer:us-east-1:123:profile/abc", "region": "us-east-1",
		})
	}))
	defer srv.Close()
	old := kiroTokenURL
	kiroTokenURL = srv.URL
	defer func() { kiroTokenURL = old }()

	oldPorts := kiroPorts
	kiroPorts = []int{freePort(t)}
	defer func() { kiroPorts = oldPorts }()
	s, err := StartLogin("kiro")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	u, err := url.Parse(s.LoginURL)
	if err != nil {
		t.Fatalf("login url: %v", err)
	}
	if u.Host != "app.kiro.dev" || u.Path != "/signin" {
		t.Fatalf("login url = %s", s.LoginURL)
	}
	q := u.Query()
	if q.Get("state") == "" || q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" || q.Get("redirect_from") != "KiroIDE" {
		t.Fatalf("login 参数不完整: %v", q)
	}
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Host == "" {
		t.Fatalf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	cb := redirect.String() + "?code=kiro-code&state=" + url.QueryEscape(q.Get("state")) + "&login_option=builder-id"
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(cb)
		if err == nil {
			resp.Body.Close()
		}
		for i := 0; i < 20 && GetLogin(s.ID).State == "pending"; i++ {
			time.Sleep(50 * time.Millisecond)
		}
		if GetLogin(s.ID).State != "pending" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	got := GetLogin(s.ID)
	if got.State != "done" {
		t.Fatalf("state = %s err=%s", got.State, got.Err)
	}
	creds := Credentials(s.ID)
	if creds["access_token"] != "kiro-access" || creds["refresh_token"] != "kiro-refresh" {
		t.Fatalf("凭证不对: %+v", creds)
	}
	if !strings.Contains(creds["profile_arn"], "profile/abc") || creds["region"] != "us-east-1" {
		t.Fatalf("profile/region 不对: %+v", creds)
	}
	if !strings.HasPrefix(creds["base_url"], "https://q.us-east-1") {
		t.Fatalf("base_url = %q", creds["base_url"])
	}
	if !MarkCommitted(s.ID, "acct-1") || MarkCommitted(s.ID, "acct-2") {
		t.Fatal("提交标记应只成功一次")
	}
}

func TestStateMismatchAndCancel(t *testing.T) {
	oldPorts := kiroPorts
	kiroPorts = []int{freePort(t)}
	defer func() { kiroPorts = oldPorts }()
	s, err := StartLogin("kiro")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	u, _ := url.Parse(s.LoginURL)
	redirect, _ := url.Parse(u.Query().Get("redirect_uri"))
	resp, err := http.Get(redirect.String() + "?code=x&state=wrong")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	resp.Body.Close()
	if got := GetLogin(s.ID); got.State != "failed" || !strings.Contains(got.Err, "状态") {
		t.Fatalf("state 不匹配应失败: %+v", got)
	}

	s2, err := StartLogin("kiro")
	if err != nil {
		t.Fatalf("StartLogin(2): %v", err)
	}
	CancelLogin(s2.ID)
	if got := GetLogin(s2.ID); got.State != "failed" || got.Err != "用户取消" {
		t.Fatalf("取消失败: %+v", got)
	}
}

func TestRejectsOtherProvider(t *testing.T) {
	if _, err := StartLogin("zed"); err == nil {
		t.Fatal("非 Kiro 渠道应报错")
	}
}
