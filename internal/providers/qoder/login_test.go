package qoder

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// TestPortalLoginFlow 覆盖设备选择页 + nonce/verifier 轮询：
// 开始返回可打开的登录页；404 视为等待；拿到 token 后会话完成并产出凭证。
func TestPortalLoginFlow(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if q.Get("nonce") == "" || q.Get("verifier") == "" || q.Get("challenge_method") != "S256" {
			t.Errorf("轮询参数不完整: %v", q)
		}
		if calls < 2 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "qoder-device-token", "user_id": "u-1", "refresh_token": "rt-1",
		})
	}))
	defer srv.Close()
	oldPoll := qoderPortalPollBase
	qoderPortalPollBase = srv.URL
	defer func() { qoderPortalPollBase = oldPoll }()

	s, err := StartPortalLogin("qoder-intl")
	if err != nil {
		t.Fatalf("StartPortalLogin: %v", err)
	}
	u, err := url.Parse(s.LoginURL)
	if err != nil {
		t.Fatalf("login url: %v", err)
	}
	if u.Host != "qoder.com" || u.Path != "/device/selectAccounts" {
		t.Fatalf("login url 不对: %s", s.LoginURL)
	}
	q := u.Query()
	if q.Get("nonce") == "" || q.Get("challenge") == "" || q.Get("challenge_method") != "S256" ||
		q.Get("redirect_uri") != qoderRedirectURI || q.Get("machine_id") == "" {
		t.Fatalf("login 参数不完整: %v", q)
	}

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if GetPortalLogin(s.ID).State == "done" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	got := GetPortalLogin(s.ID)
	if got.State != "done" {
		t.Fatalf("state = %s err=%s", got.State, got.Err)
	}
	creds := PortalCredentials(s.ID)
	if creds["token"] != "qoder-device-token" || creds["refresh_token"] != "rt-1" ||
		creds["user_id"] != "u-1" || creds["machine_id"] == "" {
		t.Fatalf("凭证不对: %+v", creds)
	}
	if !MarkPortalCommitted(s.ID, "acct-1") {
		t.Fatal("首次提交应成功")
	}
	if MarkPortalCommitted(s.ID, "acct-2") {
		t.Fatal("重复提交应被拒绝")
	}
}

func TestPortalLoginRejectsBadProvider(t *testing.T) {
	if _, err := StartPortalLogin("claude-sub"); err == nil {
		t.Fatal("非 Qoder 渠道应报错")
	}
	if _, err := StartPortalLogin("missing"); err == nil {
		t.Fatal("未注册渠道应报错")
	}
}
