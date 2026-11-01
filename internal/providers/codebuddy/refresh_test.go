package codebuddy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestCodeBuddyRefreshRotatesToken(t *testing.T) {
	var gotRefresh, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != cbRefreshPath {
			http.NotFound(w, r)
			return
		}
		gotRefresh = r.Header.Get("X-Refresh-Token")
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"code":0,"data":{"accessToken":"new-tok","refreshToken":"new-rt"}}`)
	}))
	t.Cleanup(srv.Close)

	p := newProvider(cnProfile)
	env := provider.NewAccountEnv("acc-cb", p.profile.id)
	env.Credentials = map[string]string{
		"base_url":      srv.URL,
		"token":         "old-tok",
		"refresh_token": "old-rt",
		"account_uid":   "u-1",
	}
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}

	draft, err := p.RefreshCredential(context.Background(), env)
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if gotAuth != "Bearer old-tok" || gotRefresh != "old-rt" {
		t.Fatalf("刷新请求头不符: auth=%q refresh=%q", gotAuth, gotRefresh)
	}
	if draft.Credentials["token"] != "new-tok" || draft.Credentials["refresh_token"] != "new-rt" {
		t.Fatalf("新凭证不符: %+v", draft.Credentials)
	}
	if draft.Credentials["account_uid"] != "u-1" {
		t.Fatalf("既有凭证未保留: %+v", draft.Credentials)
	}
}

func TestCodeBuddyRefreshRejectsInvalidRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"code":401,"message":"bad refresh"}`)
	}))
	t.Cleanup(srv.Close)

	p := newProvider(cnProfile)
	env := provider.NewAccountEnv("acc-cb", p.profile.id)
	env.Credentials = map[string]string{"base_url": srv.URL, "token": "t", "refresh_token": "r"}
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}

	_, err := p.RefreshCredential(context.Background(), env)
	if err == nil {
		t.Fatal("期望刷新失败")
	}
	ae, ok := err.(*ir.APIError)
	if !ok || ae.Code != ir.ErrCodeUpstreamAuth {
		t.Fatalf("错误不符: %v", err)
	}
}

func TestCodeBuddyRefreshBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":500,"message":"internal"}`)
	}))
	t.Cleanup(srv.Close)

	p := newProvider(intlProfile)
	env := provider.NewAccountEnv("acc-cb", p.profile.id)
	env.Credentials = map[string]string{"base_url": srv.URL, "token": "t", "refresh_token": "r"}
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}

	_, err := p.RefreshCredential(context.Background(), env)
	if err == nil {
		t.Fatal("期望刷新失败")
	}
	ae, ok := err.(*ir.APIError)
	if !ok || ae.Code != ir.ErrCodeUpstreamFailed {
		t.Fatalf("错误不符: %v", err)
	}
}
