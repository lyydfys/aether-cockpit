package gateway

import (
	"net/http/httptest"
	"testing"
)

func TestRemoteProviderFor(t *testing.T) {
	cases := []struct {
		protocol string
		wantID   string
		wantBase string
		wantKey  string
		wantVal  string
	}{
		{"", "openai-compat", "https://b.example/v1", "api_key", "k"},
		{"responses", "codex", "https://b.example/v1", "access_token", "k"},
		{"anthropic", "claude-sub", "https://b.example", "access_token", "k"},
		{"gemini", "gemini-compat", "https://b.example", "api_key", "k"},
		{"Gemini", "gemini-compat", "https://b.example", "api_key", "k"},
	}
	for _, c := range cases {
		id, creds := remoteProviderFor(c.protocol, "https://b.example/", "k")
		if id != c.wantID {
			t.Errorf("%q: id = %q, want %q", c.protocol, id, c.wantID)
			continue
		}
		if got := creds["base_url"]; got != c.wantBase {
			t.Errorf("%q: base_url = %q, want %q", c.protocol, got, c.wantBase)
		}
		if got := creds[c.wantKey]; got != c.wantVal {
			t.Errorf("%q: %s = %q, want %q", c.protocol, c.wantKey, got, c.wantVal)
		}
	}
}

func TestExtractTokenGoogleAPIKey(t *testing.T) {
	req := httptest.NewRequest("POST", "/v1beta/models/x:generateContent", nil)
	req.Header.Set("x-goog-api-key", "g-key")
	if got := extractToken(req); got != "g-key" {
		t.Fatalf("x-goog-api-key = %q", got)
	}
	req.Header.Set("Authorization", "Bearer b-key")
	if got := extractToken(req); got != "b-key" {
		t.Fatalf("Authorization should win, got %q", got)
	}
}
