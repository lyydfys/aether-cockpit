package minimax

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func TestDeviceFlow(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/oauth2/device/code":
			if r.PostForm.Get("client_id") != "mcode-public" || r.PostForm.Get("code_challenge_method") != "S256" || r.PostForm.Get("audience") != "agent-backend" {
				t.Errorf("device/code form = %v", r.PostForm)
			}
			_, _ = io.WriteString(w, `{"device_code":"dc1","user_code":"UC-123","verification_uri":"https://example.com/verify","expires_in":600,"interval":1}`)
		case "/oauth2/token":
			if r.PostForm.Get("grant_type") == "refresh_token" {
				_, _ = io.WriteString(w, `{"access_token":"at2","refresh_token":"rt2","expires_in":3600}`)
				return
			}
			polls++
			if polls == 1 {
				_, _ = io.WriteString(w, `{"status":"pending"}`)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"at1","refresh_token":"rt1","expires_in":3600}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := AccountBaseOverride
	AccountBaseOverride = srv.URL
	defer func() { AccountBaseOverride = old }()

	flow, err := StartDevice(context.Background(), "minimax-code", nil)
	if err != nil {
		t.Fatal(err)
	}
	if flow.UserCode != "UC-123" || flow.URL == "" || flow.Interval != time.Second {
		t.Fatalf("flow = %#v", flow)
	}
	if _, state, err := PollDevice(context.Background(), "minimax-code", flow, nil); err != nil || state != "pending" {
		t.Fatalf("pending: %q %v", state, err)
	}
	ts, state, err := PollDevice(context.Background(), "minimax-code", flow, nil)
	if err != nil || state != "done" || ts.Access != "at1" || ts.Refresh != "rt1" {
		t.Fatalf("done: %#v %q %v", ts, state, err)
	}
	if ts.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("expires = %d", ts.ExpiresAt)
	}

	rt, err := RefreshTokens(context.Background(), "minimax-code", "rt1", srv.URL, nil)
	if err != nil || rt.Access != "at2" || rt.Refresh != "rt2" {
		t.Fatalf("refresh = %#v %v", rt, err)
	}
}

func TestRefreshCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.URL.Path != "/oauth2/token" || r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "rt-old" {
			t.Errorf("refresh form = %v", r.PostForm)
		}
		_, _ = io.WriteString(w, `{"access_token":"at-new","refresh_token":"rt-new","expires_in":3600}`)
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("a", "minimax-code")
	env.Credentials = map[string]string{"access_token": "old", "refresh_token": "rt-old", "account_base_url": srv.URL}
	p := &Provider{profile: cnProfile}
	draft, err := p.RefreshCredential(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if env.GetCredential("access_token") != "at-new" || env.GetCredential("refresh_token") != "rt-new" {
		t.Fatalf("env = %v", env.Credentials)
	}
	if draft.ExpiresAt <= time.Now().Unix() || draft.Credentials["access_token"] != "at-new" {
		t.Fatalf("draft = %#v", draft)
	}
	if _, err := time.Parse(time.RFC3339, draft.Credentials["expires_at"]); err != nil {
		t.Fatalf("expires_at = %q", draft.Credentials["expires_at"])
	}
}
