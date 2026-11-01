package zed

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func TestNativeSigninCallback(t *testing.T) {
	s, err := StartLogin()
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	u, err := url.Parse(s.LoginURL)
	if err != nil {
		t.Fatalf("login url: %v", err)
	}
	if u.Host != "zed.dev" || u.Path != "/native_app_signin" {
		t.Fatalf("login url = %s", s.LoginURL)
	}
	port := u.Query().Get("native_app_port")
	pubB64 := u.Query().Get("native_app_public_key")
	if port == "" || pubB64 == "" {
		t.Fatalf("login 参数不完整: %v", u.Query())
	}
	pubDER, err := base64.RawURLEncoding.DecodeString(pubB64)
	if err != nil {
		t.Fatalf("public key b64: %v", err)
	}
	pub, err := x509.ParsePKCS1PublicKey(pubDER)
	if err != nil {
		t.Fatalf("parse public key: %v", err)
	}
	enc, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, []byte("zed-access-token"), nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	cb := "http://127.0.0.1:" + port + "/?user_id=user-1&access_token=" + url.QueryEscape(base64.RawURLEncoding.EncodeToString(enc))
	resp, err := http.Get(cb)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	resp.Body.Close()

	deadline := time.Now().Add(3 * time.Second)
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
	creds := Credentials(s.ID)
	if creds["user_id"] != "user-1" || creds["access_token"] != "zed-access-token" {
		t.Fatalf("凭证不对: %+v", creds)
	}
	if !MarkCommitted(s.ID, "acct-1") || MarkCommitted(s.ID, "acct-2") {
		t.Fatal("提交标记应只成功一次")
	}
}

func TestCancelLogin(t *testing.T) {
	s, err := StartLogin()
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	CancelLogin(s.ID)
	got := GetLogin(s.ID)
	if got.State != "failed" || got.Err != "用户取消" {
		t.Fatalf("cancel 后 state=%s err=%s", got.State, got.Err)
	}
}

func TestQueryQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "user-1 zed-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plan": "pro",
			"current_usage": map[string]any{
				"token_spend":      map[string]any{"used": 40, "limit": 100},
				"edit_predictions": map[string]any{"used": 5, "limit": 50},
			},
		})
	}))
	defer srv.Close()
	old := zedCloudURL
	zedCloudURL = srv.URL
	defer func() { zedCloudURL = old }()

	env := provider.NewAccountEnv("zed-test", channelID)
	env.Credentials = map[string]string{"user_id": "user-1", "access_token": "zed-token"}
	env.HTTPClient = &http.Client{}
	q, err := (&Provider{}).QueryQuota(context.Background(), env)
	if err != nil {
		t.Fatalf("QueryQuota: %v", err)
	}
	if q.Plan != "pro" || q.Total != 100 || q.Used != 40 || q.Remaining != 60 {
		t.Fatalf("总额度不对: %+v", q)
	}
	if len(q.Windows) != 2 || q.Windows[0].Name != "Token spend" || q.Windows[1].Used != 10 {
		t.Fatalf("窗口不对: %+v", q.Windows)
	}
}
