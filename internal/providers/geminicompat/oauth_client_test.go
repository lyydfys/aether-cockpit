package geminicompat

import (
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestGeminiOAuthClientCredentialsRequireLocalConfiguration(t *testing.T) {
	t.Setenv("AETHER_GEMINI_CLI_OAUTH_CLIENT_ID", "")
	t.Setenv("AETHER_GEMINI_CLI_OAUTH_CLIENT_SECRET", "")
	env := provider.NewAccountEnv("test-account", "google")
	if _, _, err := geminiOAuthClientCredentials(env); err == nil || !strings.Contains(err.Error(), "AETHER_GEMINI_CLI_OAUTH_CLIENT_ID") {
		t.Fatalf("missing credentials error = %v", err)
	}
}

func TestGeminiOAuthClientCredentialsUseEnvironment(t *testing.T) {
	t.Setenv("AETHER_GEMINI_CLI_OAUTH_CLIENT_ID", "environment-id")
	t.Setenv("AETHER_GEMINI_CLI_OAUTH_CLIENT_SECRET", "environment-secret")
	env := provider.NewAccountEnv("test-account", "google")
	clientID, clientSecret, err := geminiOAuthClientCredentials(env)
	if err != nil || clientID != "environment-id" || clientSecret != "environment-secret" {
		t.Fatalf("credentials = %q/%q, err = %v", clientID, clientSecret, err)
	}
}
