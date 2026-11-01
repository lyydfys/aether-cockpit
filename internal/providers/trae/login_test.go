package trae

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPortalLoginFlow(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cloudide/api/v3/trae/oauth/ExchangeToken" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"Result": map[string]any{"AccessToken": "trae-token", "UserID": "trae-user"},
		})
	}))
	defer srv.Close()
	old := traeAccountOrigins["trae-cn"]
	traeAccountOrigins["trae-cn"] = srv.URL
	defer func() { traeAccountOrigins["trae-cn"] = old }()

	s, err := StartLogin("trae-cn")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	u, err := url.Parse(s.LoginURL)
	if err != nil {
		t.Fatalf("login url: %v", err)
	}
	if u.Host != "www.trae.cn" || u.Path != "/authorization" {
		t.Fatalf("login url = %s", s.LoginURL)
	}
	q := u.Query()
	for _, k := range []string{"client_id", "code_challenge", "auth_callback_url", "machine_id", "device_id", "x_device_type"} {
		if q.Get(k) == "" {
			t.Fatalf("缺少参数 %s: %s", k, s.LoginURL)
		}
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("auth_from") != "trae" {
		t.Fatalf("参数不对: %v", q)
	}
	cb, err := url.Parse(q.Get("auth_callback_url"))
	if err != nil {
		t.Fatalf("callback url: %v", err)
	}
	resp, err := http.Get(cb.String() + "?code=trae-code")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	resp.Body.Close()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if GetLogin(s.ID).State == "done" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	got := GetLogin(s.ID)
	if got.State != "done" {
		t.Fatalf("state = %s err=%s", got.State, got.Err)
	}
	if gotBody["AuthCode"] != "trae-code" || gotBody["ClientID"] != traeClientID {
		t.Fatalf("ExchangeToken 请求不对: %+v", gotBody)
	}
	creds := Credentials(s.ID)
	if creds["token"] != "trae-token" || creds["user_id"] != "trae-user" || creds["region"] != "CN" {
		t.Fatalf("凭证不对: %+v", creds)
	}
	if !strings.HasPrefix(creds["host"], "https://www.trae.cn") {
		t.Fatalf("host = %q", creds["host"])
	}
	if !MarkCommitted(s.ID, "acct-1") || MarkCommitted(s.ID, "acct-2") {
		t.Fatal("提交标记应只成功一次")
	}
}

func TestCancelAndReject(t *testing.T) {
	s, err := StartLogin("trae-intl")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	CancelLogin(s.ID)
	if got := GetLogin(s.ID); got.State != "failed" || got.Err != "用户取消" {
		t.Fatalf("取消失败: %+v", got)
	}
	if _, err := StartLogin("kiro"); err == nil {
		t.Fatal("非 Trae 渠道应报错")
	}
}
