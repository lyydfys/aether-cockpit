package windsurf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestLoginAndRefreshChain(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/password", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["email"] != "dev@example.com" || body["password"] != "pw" || body["product"] != "Windsurf" {
			t.Errorf("password body = %+v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "auth1_test", "email": "dev@example.com"})
	})
	mux.HandleFunc("/postauth", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-devin-auth1-token") != "auth1_test" {
			t.Errorf("auth1 header = %q", r.Header.Get("x-devin-auth1-token"))
		}
		raw := append(pbString(1, "sess-1"), pbString(3, "auth1-back")...)
		raw = append(raw, pbString(4, "acct-1")...)
		raw = append(raw, pbString(5, "org-1")...)
		_, _ = w.Write(raw)
	})
	mux.HandleFunc("/ott", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-devin-session-token") != "sess-1" || r.Header.Get("x-devin-account-id") != "acct-1" {
			t.Errorf("ott headers = %v", r.Header)
		}
		_, _ = w.Write(pbString(1, "ott-1"))
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "connect-es/1.5.0" {
			t.Errorf("register UA = %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte("junk devin-session-token$ide-1"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	old := []string{passwordLoginURL, postAuthURL, getOTTURL, registerUserURL}
	passwordLoginURL = srv.URL + "/password"
	postAuthURL = srv.URL + "/postauth"
	getOTTURL = srv.URL + "/ott"
	registerUserURL = srv.URL + "/register"
	defer func() {
		passwordLoginURL, postAuthURL, getOTTURL, registerUserURL = old[0], old[1], old[2], old[3]
	}()

	login, err := LoginWithPassword(context.Background(), "dev@example.com", "pw")
	if err != nil {
		t.Fatalf("LoginWithPassword: %v", err)
	}
	if login.Auth1Token != "auth1_test" || login.Email != "dev@example.com" {
		t.Fatalf("login = %+v", login)
	}
	full, err := FullRefresh(context.Background(), login.Auth1Token)
	if err != nil {
		t.Fatalf("FullRefresh: %v", err)
	}
	if full.SessionToken != "sess-1" || full.Auth1Token != "auth1-back" || full.IDEToken != "devin-session-token$ide-1" {
		t.Fatalf("full = %+v", full)
	}
	if full.AccountID != "acct-1" || full.OrgID != "org-1" {
		t.Fatalf("account/org = %+v", full)
	}

	env := provider.NewAccountEnv("wf-test", channelID)
	env.Credentials = map[string]string{"auth1_token": "auth1_test", "email": "dev@example.com"}
	draft, err := (&Provider{}).RefreshCredential(context.Background(), env)
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if draft.Credentials["ide_token"] == "" || draft.Credentials["session_token"] != "sess-1" || draft.Credentials["email"] != "dev@example.com" {
		t.Fatalf("draft = %+v", draft.Credentials)
	}
}

func TestManifestAndValidation(t *testing.T) {
	m := (&Provider{}).Manifest()
	if m.ID != "windsurf" || m.Family != "windsurf" || m.Capabilities.Chat {
		t.Fatalf("manifest = %+v", m)
	}
	if !m.Capabilities.Refresh {
		t.Fatal("Refresh 能力未声明")
	}
	if err := ValidateAuth1("bad"); err == nil {
		t.Fatal("非法 auth1 应报错")
	}
	if err := ValidateAuth1("auth1_x"); err != nil {
		t.Fatalf("合法 auth1 报错: %v", err)
	}
	if _, err := LoginWithPassword(context.Background(), "", ""); err == nil || !strings.Contains(err.Error(), "邮箱") {
		t.Fatalf("空凭证应报错: %v", err)
	}
}
