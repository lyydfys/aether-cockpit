package reverse

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestCodexOfficialBinding(t *testing.T) {
	mkIDToken := func(claims map[string]any) string {
		payload, _ := json.Marshal(claims)
		return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	}
	creds := map[string]string{
		"account_id": "acc-1",
		"refresh_token": "rt",
		"id_token": mkIDToken(map[string]any{
			"email": "dev@example.com",
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": "acc-oidc",
				"chatgpt_plan_type":  "plus",
			},
		}),
		"expires_at": "2026-11-01T00:00:00Z",
	}
	b := codexOfficialBinding(creds)
	if b == nil || !b.Official {
		t.Fatalf("binding = %+v", b)
	}
	if b.AccountID != "acc-1" || b.Email != "dev@example.com" || b.Plan != "plus" {
		t.Fatalf("binding = %+v", b)
	}
	if b.ExpiresAt == 0 {
		t.Fatal("expires_at 未解析")
	}

	// 没有 account_id 时从 id_token 取。
	relay := map[string]string{"base_url": "https://relay.example", "access_token": "tok"}
	if b := codexOfficialBinding(relay); b.Official {
		t.Fatalf("中转账号不应判为官方: %+v", b)
	}
	onlyToken := map[string]string{
		"id_token": mkIDToken(map[string]any{
			"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acc-oidc"},
		}),
	}
	if b := codexOfficialBinding(onlyToken); b.AccountID != "acc-oidc" {
		t.Fatalf("应回退到 id_token 的 account id: %+v", b)
	}
}
