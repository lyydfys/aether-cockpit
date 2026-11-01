package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"aether-cockpit/internal/provider"
)

type bindingStubProvider struct{}

func (bindingStubProvider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:          "binding-stub",
		DisplayName: "Binding Stub",
		Capabilities: provider.CapabilitySet{
			Chat: true, OfficialBinding: true,
		},
	}
}

func (bindingStubProvider) OfficialBinding(creds map[string]string) *provider.OfficialBinding {
	return &provider.OfficialBinding{
		Official:  creds["official"] == "1",
		AccountID: creds["account_id"],
		Email:     creds["email"],
		Plan:      "plus",
	}
}

var bindingStubOnce sync.Once

func TestAccountBindingEndpoint(t *testing.T) {
	bindingStubOnce.Do(func() { provider.Register(bindingStubProvider{}) })
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	env := provider.NewAccountEnv("acc-b1", "binding-stub")
	env.Credentials = map[string]string{"official": "1", "account_id": "acc-oidc", "email": "dev@example.com"}
	g.Pool().Add(env)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/accounts/acc-b1/binding", nil)
	req.SetPathValue("id", "acc-b1")
	g.handleAccountBinding(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("binding status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Binding *provider.OfficialBinding `json:"binding"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Binding == nil || !out.Binding.Official || out.Binding.AccountID != "acc-oidc" ||
		out.Binding.Email != "dev@example.com" || out.Binding.Plan != "plus" {
		t.Fatalf("binding = %+v", out.Binding)
	}

	// 不支持的渠道 → 409。
	plain := provider.NewAccountEnv("acc-b2", "mock-native")
	g.Pool().Add(plain)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/accounts/acc-b2/binding", nil)
	req.SetPathValue("id", "acc-b2")
	g.handleAccountBinding(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("不支持渠道应 409，实际 %d", rec.Code)
	}
}

func TestReplaceAccountCredentialsKeepsOldOnEmpty(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	env := provider.NewAccountEnv("acc-r1", "codex")
	env.Credentials = map[string]string{"access_token": "old", "refresh_token": "old-rt", "email": "old@example.com"}
	g.Pool().Add(env)

	g.replaceAccountCredentials(env, &provider.CredentialDraft{Credentials: map[string]string{
		"access_token":  "new",
		"refresh_token": "",
	}})
	if env.GetCredential("access_token") != "new" {
		t.Fatalf("access_token 未替换: %q", env.GetCredential("access_token"))
	}
	if env.GetCredential("refresh_token") != "old-rt" {
		t.Fatal("空值覆盖了旧的 refresh_token")
	}
	if env.GetCredential("email") != "old@example.com" {
		t.Fatal("未提供的字段被清空")
	}
}
