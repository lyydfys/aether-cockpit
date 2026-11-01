package reverse

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

// ChatGPT 订阅账号走 Responses 直通时必须带账号头，模型取 auto 凭证。
func TestCodexSubscriptionResponsesHeaders(t *testing.T) {
	var gotAuth, gotAccount, gotOrigin, gotModel string
	seenPath := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccount = r.Header.Get("chatgpt-account-id")
		gotOrigin = r.Header.Get("originator")
		raw, _ := io.ReadAll(r.Body)
		gotModel = string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\"}}\n\n"))
	}))
	defer srv.Close()

	p := New(Profile{
		ID: "codex", NativeResponses: true, DefaultResponsesPath: "/responses", QuotaKind: "codex",
	})
	env := provider.NewAccountEnv("sub-1", "codex")
	env.Credentials = map[string]string{
		"base_url": srv.URL, "access_token": "oat-token", "account_id": "acc-42", "model": "gpt-5-codex",
	}
	env.HTTPClient = srv.Client()

	resp, err := p.ResponsesPassthrough(context.Background(), env, []byte(`{"model":"codex/auto","input":"hi"}`), "gpt-5-codex")
	if err != nil {
		t.Fatalf("passthrough: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "response.completed") {
		t.Fatalf("body = %s", body)
	}
	if seenPath != "/responses" || gotAuth != "Bearer oat-token" || gotAccount != "acc-42" || gotOrigin != "codex_cli_rs" {
		t.Fatalf("path=%q auth=%q account=%q origin=%q", seenPath, gotAuth, gotAccount, gotOrigin)
	}
	if !strings.Contains(gotModel, `"model":"gpt-5-codex"`) {
		t.Fatalf("body model 未替换: %s", gotModel)
	}
}
