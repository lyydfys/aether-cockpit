package geminicompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func fakeIDToken(email string) string {
	payload, _ := json.Marshal(map[string]any{"email": email})
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

func TestGeminiOAuthLocalImport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oauth_creds.json")
	raw, _ := json.Marshal(map[string]any{
		"access_token": "at", "refresh_token": "rt", "id_token": fakeIDToken("g@example.com"),
		"expiry_date": time.Now().Add(time.Hour).UnixMilli(), "token_type": "Bearer",
	})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AETHER_GEMINI_OAUTH_CREDS", path)
	p := &Provider{cfg: googleConfig}
	drafts, err := p.DiscoverLocalAccounts(context.Background())
	if err != nil || len(drafts) != 1 {
		t.Fatalf("drafts=%+v err=%v", drafts, err)
	}
	d := drafts[0]
	if !strings.Contains(d.Label, "g@example.com") || d.Credentials["auth_mode"] != "oauth" || d.Credentials["refresh_token"] != "rt" {
		t.Fatalf("draft = %+v", d)
	}
	generic := &Provider{cfg: defaultGeminiConfig}
	if _, err := generic.DiscoverLocalAccounts(context.Background()); err == nil {
		t.Fatal("gemini-compat should not expose local OAuth import")
	}
}

func TestGeminiOAuthCodeAssistChat(t *testing.T) {
	var tokenCalls, loadCalls, chatCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			tokenCalls++
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "rt-old" {
				t.Errorf("token form = %v", r.Form)
			}
			_, _ = io.WriteString(w, `{"access_token":"at-new","refresh_token":"rt-new","expires_in":3600}`)
		case strings.HasSuffix(r.URL.Path, ":loadCodeAssist"):
			loadCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer at-new" {
				t.Errorf("load auth = %q", got)
			}
			raw, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(raw), `"pluginType":"GEMINI"`) {
				t.Errorf("load body = %s", raw)
			}
			_, _ = io.WriteString(w, `{"cloudaicompanionProject":"proj-x","currentTier":{"id":"LEGACY"}}`)
		case strings.Contains(r.URL.Path, ":streamGenerateContent"):
			chatCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer at-new" {
				t.Errorf("chat auth = %q", got)
			}
			if got := r.URL.Query().Get("alt"); got != "sse" {
				t.Errorf("alt = %q", got)
			}
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			if body["model"] != "gemini-2.5-flash" || body["project"] != "proj-x" || body["request"] == nil {
				t.Errorf("code assist body = %s", raw)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"gemini-ok\"}]},\"finishReason\":\"STOP\"}]}}\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := &Provider{cfg: Config{ID: "google", OAuthEnabled: true, BaseURL: srv.URL}}
	env := provider.NewAccountEnv("acc-gemini", "google")
	env.Credentials = map[string]string{
		"auth_mode": "oauth", "refresh_token": "rt-old",
		"expires_at": time.Now().Add(-time.Hour).Format(time.RFC3339),
		"base_url":   srv.URL, "oauth_token_url": srv.URL + "/token",
	}
	env.SetHTTPClient(srv.Client())
	req := &ir.UnifiedChatRequest{
		Model: "google/gemini-2.5-flash", UpstreamModel: "gemini-2.5-flash", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	}
	ch, err := p.ChatStream(context.Background(), env, req)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for ev := range ch {
		if ev.Type == ir.EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
		if ev.Type == ir.EventTextDelta {
			text.WriteString(ev.TextDelta)
		}
	}
	if text.String() != "gemini-ok" {
		t.Fatalf("text = %q", text.String())
	}
	if tokenCalls != 1 || loadCalls != 1 || chatCalls != 1 {
		t.Fatalf("calls token=%d load=%d chat=%d", tokenCalls, loadCalls, chatCalls)
	}
	if env.GetCredential("access_token") != "at-new" || env.GetCredential("project_id") != "proj-x" {
		t.Fatalf("cached creds = %v", env.Credentials)
	}
}

func TestGeminiManifestOAuthFields(t *testing.T) {
	keys := map[string]bool{}
	for _, f := range (&Provider{cfg: googleConfig}).Manifest().AuthFormSchema {
		keys[f.Key] = true
	}
	for _, k := range []string{"access_token", "refresh_token", "project_id"} {
		if !keys[k] {
			t.Fatalf("missing %s: %v", k, keys)
		}
	}
	auths := (&Provider{cfg: googleConfig}).Manifest().SupportedAuths
	if len(auths) < 2 || auths[0] != provider.AuthLocalImport {
		t.Fatalf("auths = %v", auths)
	}
}

func TestGeminiCodeAssistLicenseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":loadCodeAssist") {
			_, _ = io.WriteString(w, `{"allowedTiers":[{"id":"standard-tier","isDefault":true}],"ineligibleTiers":[{"reasonCode":"UNSUPPORTED_CLIENT","reasonMessage":"This client is no longer supported","tierId":"free-tier"}]}`)
			return
		}
		t.Errorf("unexpected call %s (should not onboard/license-fail)", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	p := &Provider{cfg: Config{ID: "google", OAuthEnabled: true}}
	env := provider.NewAccountEnv("acc-lic", "google")
	env.Credentials = map[string]string{
		"auth_mode": "oauth", "access_token": "at", "base_url": srv.URL,
		"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
	}
	env.SetHTTPClient(srv.Client())
	_, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "google/gemini-2.5-flash", UpstreamModel: "gemini-2.5-flash",
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "Code Assist 不可用") {
		t.Fatalf("err = %v", err)
	}
}

func TestGeminiAIStudioOAuth(t *testing.T) {
	var gotPath, gotAuth string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"aistudio-ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer srv.Close()
	p := &Provider{cfg: Config{ID: "google", OAuthEnabled: true}}
	env := provider.NewAccountEnv("acc-ai", "google")
	env.Credentials = map[string]string{
		"auth_mode": "oauth", "oauth_type": "ai_studio", "access_token": "at",
		"base_url": srv.URL, "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
	}
	env.SetHTTPClient(srv.Client())
	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "google/gemini-2.5-flash", UpstreamModel: "gemini-2.5-flash", Stream: true,
		Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for ev := range ch {
		if ev.Type == ir.EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
		if ev.Type == ir.EventTextDelta {
			text.WriteString(ev.TextDelta)
		}
	}
	if text.String() != "aistudio-ok" || gotAuth != "Bearer at" {
		t.Fatalf("text=%q auth=%q", text.String(), gotAuth)
	}
	if !strings.Contains(gotPath, "/v1beta/models/gemini-2.5-flash:streamGenerateContent") {
		t.Fatalf("path = %q", gotPath)
	}
	if body["contents"] == nil || body["model"] != nil {
		t.Fatalf("AI Studio body should be raw Gemini request: %+v", body)
	}
}
