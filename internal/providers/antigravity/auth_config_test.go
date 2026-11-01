package antigravity

import (
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestOAuthClientCredentialsRequireLocalConfiguration(t *testing.T) {
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID", "")
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "")
	env := provider.NewAccountEnv("test-account", "antigravity")
	if _, _, err := antigravityOAuthClientCredentials(env); err == nil || !strings.Contains(err.Error(), "AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID") {
		t.Fatalf("missing credentials error = %v", err)
	}
}

func TestOAuthClientCredentialsUseEnvironment(t *testing.T) {
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID", "environment-id")
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "environment-secret")
	env := provider.NewAccountEnv("test-account", "antigravity")
	clientID, clientSecret, err := antigravityOAuthClientCredentials(env)
	if err != nil || clientID != "environment-id" || clientSecret != "environment-secret" {
		t.Fatalf("credentials = %q/%q, err = %v", clientID, clientSecret, err)
	}
}

func TestOAuthClientCredentialsPreferAccountValues(t *testing.T) {
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID", "environment-id")
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "environment-secret")
	env := provider.NewAccountEnv("test-account", "antigravity")
	env.Credentials = map[string]string{"oauth_client_id": "account-id", "oauth_client_secret": "account-secret"}
	clientID, clientSecret, err := antigravityOAuthClientCredentials(env)
	if err != nil || clientID != "account-id" || clientSecret != "account-secret" {
		t.Fatalf("credentials = %q/%q, err = %v", clientID, clientSecret, err)
	}
}
