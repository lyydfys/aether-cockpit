package geminicompat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestListModelsGemini(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"models":[{"name":"models/gemini-2.5-pro"},{"name":"models/gemini-2.5-flash"},{"name":"models/gemini-2.5-pro"}]}`)
	}))
	defer srv.Close()

	p := &Provider{cfg: Config{ID: "test-gemini", BaseURL: srv.URL}}
	env := provider.NewAccountEnv("acc", "test-gemini")
	env.Credentials = map[string]string{"api_key": "gk-test"}
	env.HTTPClient = srv.Client()
	if !p.SupportsModelList(env) {
		t.Fatal("API Key 账号应支持 Gemini 模型列表")
	}
	ids, err := p.ListModels(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "gemini-2.5-pro" || ids[1] != "gemini-2.5-flash" {
		t.Fatalf("ids = %v", ids)
	}
	if gotPath != "/v1beta/models" || gotKey != "gk-test" {
		t.Fatalf("path=%q key=%q", gotPath, gotKey)
	}

	oauth := provider.NewAccountEnv("acc2", "test-gemini")
	oauth.Credentials = map[string]string{"access_token": "at", "refresh_token": "rt"}
	if p.SupportsModelList(oauth) {
		t.Fatal("OAuth 登录态不应走公开模型列表")
	}
}
