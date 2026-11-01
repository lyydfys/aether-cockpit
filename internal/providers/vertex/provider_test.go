package vertex

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestChatStreamADCRefresh(t *testing.T) {
	var tokenCalls int
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenCalls++
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "rt" {
				t.Errorf("token form = %v", r.Form)
			}
			_, _ = io.WriteString(w, `{"access_token":"tok-1","expires_in":3600}`)
			return
		}
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"vertex-ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer srv.Close()

	adc, _ := json.Marshal(ADCFile{
		Type: "authorized_user", ClientID: "cid", ClientSecret: "cs", RefreshToken: "rt",
		TokenURI: srv.URL + "/token",
	})
	env := provider.NewAccountEnv("acc", "vertex")
	env.Credentials = map[string]string{
		"project": "proj", "location": "global", "base_url": srv.URL, "adc_json": string(adc),
	}
	env.SetHTTPClient(srv.Client())

	p := &Provider{}
	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model:              "vertex/gemini-2.5-pro",
		DownstreamProtocol: ir.ProtocolOpenAIChat,
		Items:              []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
		Stream:             true,
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
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
	if text.String() != "vertex-ok" {
		t.Errorf("text = %q", text.String())
	}
	if tokenCalls != 1 {
		t.Errorf("token calls = %d, want 1", tokenCalls)
	}
	wantPath := "/v1/projects/proj/locations/global/publishers/google/models/gemini-2.5-pro:streamGenerateContent"
	if gotPath != wantPath {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tok-1" {
		t.Errorf("auth = %q", gotAuth)
	}
	if env.GetCredential("access_token") != "tok-1" {
		t.Errorf("token not cached on env")
	}
}

func TestServiceAccountJWTRefresh(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY", Bytes: mustPKCS8(t, priv),
	})

	var tokenCalls int
	var gotPath, gotAuth, tokenURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenCalls++
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
			}
			assertion := r.Form.Get("assertion")
			parts := strings.Split(assertion, ".")
			if len(parts) != 3 {
				t.Fatalf("JWT parts = %d", len(parts))
			}
			claimsRaw, _ := base64.RawURLEncoding.DecodeString(parts[1])
			var claims map[string]any
			if err := json.Unmarshal(claimsRaw, &claims); err != nil {
				t.Fatal(err)
			}
			if claims["iss"] != "svc@example.com" || claims["aud"] != tokenURL {
				t.Errorf("claims iss/aud = %v/%v", claims["iss"], claims["aud"])
			}
			sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
			sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			if err := rsa.VerifyPKCS1v15(&priv.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
				t.Fatalf("JWT signature: %v", err)
			}
			_, _ = io.WriteString(w, `{"access_token":"svc-token","expires_in":3600}`)
			return
		}
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"svc-ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	}))
	defer srv.Close()
	tokenURL = srv.URL + "/token"

	adc, _ := json.Marshal(ADCFile{
		Type: "service_account", ProjectID: "svc-proj", ClientEmail: "svc@example.com",
		PrivateKey: string(keyPEM), TokenURI: tokenURL,
	})
	env := provider.NewAccountEnv("acc-svc", "vertex")
	env.Credentials = map[string]string{"base_url": srv.URL, "adc_json": string(adc)}
	env.SetHTTPClient(srv.Client())

	p := &Provider{}
	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		Model: "vertex/gemini-2.5-pro", Stream: true,
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
	if text.String() != "svc-ok" || tokenCalls != 1 || gotAuth != "Bearer svc-token" {
		t.Fatalf("text=%q calls=%d auth=%q", text.String(), tokenCalls, gotAuth)
	}
	if !strings.Contains(gotPath, "/projects/svc-proj/") {
		t.Fatalf("path = %q (project from service account not applied)", gotPath)
	}
	if env.GetCredential("project") != "svc-proj" {
		t.Fatalf("project = %q", env.GetCredential("project"))
	}
}

func mustPKCS8(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	raw, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseADCRejectsIncompleteServiceAccount(t *testing.T) {
	if _, err := ParseADC([]byte(`{"type":"service_account","client_email":"x"}`)); err == nil ||
		!strings.Contains(err.Error(), "private_key") {
		t.Fatalf("err = %v", err)
	}
}
