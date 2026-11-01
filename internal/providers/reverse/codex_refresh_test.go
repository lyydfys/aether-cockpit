package reverse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func testIDToken(t *testing.T, accountID string) string {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": accountID},
	})
	enc := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + ".sig"
}

func TestCodexRefreshCredential(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"id_token":      testIDToken(t, "acc-9"),
		})
	}))
	defer srv.Close()
	old := codexTokenURL
	codexTokenURL = srv.URL
	defer func() { codexTokenURL = old }()

	env := provider.NewAccountEnv("c1", "codex")
	env.Credentials = map[string]string{"refresh_token": "old-refresh", "account_id": "acc-1"}
	env.HTTPClient = srv.Client()

	draft, err := New(Profile{ID: "codex", Refresh: true}).RefreshCredential(context.Background(), env)
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if draft.Credentials["access_token"] != "new-access" || draft.Credentials["refresh_token"] != "new-refresh" {
		t.Fatalf("draft = %+v", draft.Credentials)
	}
	if draft.Credentials["account_id"] != "acc-9" {
		t.Fatalf("account_id = %q", draft.Credentials["account_id"])
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("client_id") != codexClientID || form.Get("refresh_token") != "old-refresh" {
		t.Fatalf("form = %v", form)
	}
}

func TestCodexRefreshErrors(t *testing.T) {
	env := provider.NewAccountEnv("c2", "codex")
	if _, err := New(Profile{ID: "codex"}).RefreshCredential(context.Background(), env); err == nil || !strings.Contains(err.Error(), "refresh_token") {
		t.Fatalf("missing refresh: %v", err)
	}
	env.SetCredential("refresh_token", "rt")
	if _, err := New(Profile{ID: "opencode"}).RefreshCredential(context.Background(), env); err == nil {
		t.Fatal("非 codex 渠道应拒绝")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	old := codexTokenURL
	codexTokenURL = srv.URL
	defer func() { codexTokenURL = old }()
	env.HTTPClient = srv.Client()
	if _, err := New(Profile{ID: "codex"}).RefreshCredential(context.Background(), env); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("401: %v", err)
	}
}

func TestCodexRefreshCapability(t *testing.T) {
	if !New(Profile{ID: "codex", Refresh: true}).Manifest().Capabilities.Refresh {
		t.Fatal("codex 应声明刷新能力")
	}
	if New(Profile{ID: "opencode", QuotaKind: "opencode_go"}).Manifest().Capabilities.Refresh {
		t.Fatal("opencode 不应声明刷新能力")
	}
}

func TestCodexAuthJSONImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	dir := home + string(os.PathSeparator) + ".codex"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := testIDToken(t, "acc-json")
	auth := `{"tokens":{"access_token":"json-access","refresh_token":"json-refresh","id_token":"` + id + `"}}`
	if err := os.WriteFile(dir+string(os.PathSeparator)+"auth.json", []byte(auth), 0o600); err != nil {
		t.Fatal(err)
	}

	p := New(Profile{ID: "codex"})
	drafts, err := p.DiscoverLocalAccounts(context.Background())
	if err != nil {
		t.Fatalf("DiscoverLocalAccounts: %v", err)
	}
	var found *provider.CredentialDraft
	for _, d := range drafts {
		if strings.Contains(d.Source, "auth.json") {
			found = d
		}
	}
	if found == nil {
		t.Fatalf("没有导入 auth.json: %+v", drafts)
	}
	c := found.Credentials
	if c["access_token"] != "json-access" || c["refresh_token"] != "json-refresh" ||
		c["account_id"] != "acc-json" || c["base_url"] != "https://chatgpt.com/backend-api/codex" || c["model"] != "gpt-5-codex" {
		t.Fatalf("credentials = %+v", c)
	}
}
