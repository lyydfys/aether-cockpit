package geminicompat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestEnsureOAuthTokenUsesReferenceClientDefaults(t *testing.T) {
	t.Setenv("AETHER_GEMINI_CLI_OAUTH_CLIENT_ID", "")
	t.Setenv("AETHER_GEMINI_CLI_OAUTH_CLIENT_SECRET", "")
	var gotID, gotSecret string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotID = r.Form.Get("client_id")
		gotSecret = r.Form.Get("client_secret")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"new-access","expires_in":3600}`)
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("gemini-reference-test", "google")
	env.Credentials = map[string]string{
		"refresh_token":   "refresh-test",
		"oauth_token_url": srv.URL,
	}
	env.HTTPClient = srv.Client()
	p := &Provider{cfg: googleConfig}

	token, err := p.ensureOAuthToken(context.Background(), env)
	if err != nil || token != "new-access" {
		t.Fatalf("token = %q, err = %v", token, err)
	}
	if gotID != referenceGeminiOAuthClientID || gotSecret != referenceGeminiOAuthClientSecret {
		t.Fatal("reference Gemini CLI OAuth client was not used")
	}
	if env.GetCredential("access_token") != "new-access" {
		t.Fatal("access token was not written back to the account")
	}
}
