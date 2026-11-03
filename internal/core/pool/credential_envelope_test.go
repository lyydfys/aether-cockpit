package pool

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/credential"
	"aether-cockpit/internal/core/simplecrypt"
	_ "aether-cockpit/internal/providers/mock"
)

func writeCredentialsFile(t *testing.T, path, credentialsEnc string) {
	t.Helper()
	body, err := json.Marshal(accountsFile{
		Version: 1,
		Accounts: []persistedAccount{{
			AccountID:      "acct-1",
			ProviderID:     "mock-native",
			Label:          "A",
			CredentialsEnc: credentialsEnc,
			CreatedAt:      1,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestV1CredentialsMigrateToEnvelopeV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	legacy, err := simplecrypt.Encrypt([]byte(`{"api_key":"fixture-token"}`))
	if err != nil {
		t.Fatal(err)
	}
	writeCredentialsFile(t, path, base64.StdEncoding.EncodeToString(legacy))

	key := bytes.Repeat([]byte{3}, credential.KeySize)
	m := New()
	m.SetCredentialKey(key)
	if err := m.SetStorePath(path, nil); err != nil {
		t.Fatalf("load v1: %v", err)
	}
	env, ok := m.Get("acct-1")
	if !ok || env.GetCredential("api_key") != "fixture-token" {
		t.Fatalf("v1 credentials not loaded: %#v", env)
	}

	var after accountsFile
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(after.Accounts[0].CredentialsEnc)
	if err != nil {
		t.Fatal(err)
	}
	if !credential.IsV2(raw) {
		t.Fatal("v1 file was not rewritten as envelope v2")
	}

	m2 := New()
	m2.SetCredentialKey(key)
	if err := m2.SetStorePath(path, nil); err != nil {
		t.Fatalf("reload v2: %v", err)
	}
	env2, ok := m2.Get("acct-1")
	if !ok || env2.GetCredential("api_key") != "fixture-token" {
		t.Fatal("v2 credentials not loaded with the same key")
	}
}

func TestWrongEnvelopeKeyPreservesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	keyA := bytes.Repeat([]byte{4}, credential.KeySize)
	sealed, err := credential.Seal(
		keyA,
		[]byte(`{"api_key":"fixture-token"}`),
		credential.AAD("mock-native", "acct-1"),
	)
	if err != nil {
		t.Fatal(err)
	}
	writeCredentialsFile(t, path, base64.StdEncoding.EncodeToString(sealed))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	m := New()
	m.SetCredentialKey(bytes.Repeat([]byte{5}, credential.KeySize))
	if err := m.SetStorePath(path, nil); err == nil {
		t.Fatal("wrong key must fail to load")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed load must leave the account file untouched")
	}
}
