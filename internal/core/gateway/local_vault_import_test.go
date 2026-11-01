package gateway

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCockpitEnvelope(t *testing.T, path string, key []byte, plain map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(plain)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatal(err)
	}
	ct := gcm.Seal(nil, nonce, raw, nil)
	env := map[string]any{
		"version": 1, "kind": plain["id"], "algorithm": "AES-256-GCM",
		"key_id":       "local-secure-account-storage-v1",
		"nonce":        base64.StdEncoding.EncodeToString(nonce),
		"ciphertext":   base64.StdEncoding.EncodeToString(ct),
		"encrypted_at": 1793493205,
	}
	blob, _ := json.Marshal(env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLocalVaultImport(t *testing.T) {
	root := t.TempDir()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secure-account-storage.key"), []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	writeCockpitEnvelope(t, filepath.Join(root, "accounts", "antigravity_1.json"), key, map[string]any{
		"id": "antigravity_1", "email": "ant@example.com", "name": "Anti",
		"token": map[string]any{
			"access_token": "anti-access", "refresh_token": "anti-refresh",
			"id_token": "anti-id", "token_type": "Bearer", "expiry_timestamp": float64(1793494990),
		},
	})
	writeCockpitEnvelope(t, filepath.Join(root, "codex_accounts", "codex_1.json"), key, map[string]any{
		"id": "codex_1", "email": "codex@example.com", "account_id": "acct-1",
		"tokens": map[string]any{"access_token": "codex-access", "id_token": "codex-id"},
	})
	t.Setenv("AETHER_VAULT_DATA_DIR", root)

	g := newAutoImportGateway(t)
	entries := g.localVaultEntries()
	if len(entries) != 2 {
		t.Fatalf("entries = %d: %+v", len(entries), entries)
	}
	byProvider := map[string]localVaultEntry{}
	for _, e := range entries {
		byProvider[e.ProviderID] = e
	}
	if byProvider["antigravity"].credentials["refresh_token"] != "anti-refresh" {
		t.Fatalf("antigravity creds = %+v", byProvider["antigravity"].credentials)
	}
	if byProvider["codex"].credentials["account_id"] != "acct-1" {
		t.Fatalf("codex creds = %+v", byProvider["codex"].credentials)
	}

	// 列表接口只给标签/指纹，不能出现凭证明文。
	rec := httptest.NewRecorder()
	g.handleImportApps(rec, httptest.NewRequest(http.MethodGet, "/api/importapps", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("importapps = %d %s", rec.Code, body)
	}
	if strings.Contains(body, "anti-access") || strings.Contains(body, "codex-access") {
		t.Fatal("importapps 泄漏了 local-vault 凭证")
	}
	if !strings.Contains(body, "Local Vault") || !strings.Contains(body, "ant@example.com") {
		t.Fatalf("importapps 没有列出 local-vault 账号: %s", body[:min(500, len(body))])
	}

	// 提交导入两条。
	picks := map[string]any{"picks": []any{
		map[string]any{"source": "antigravity", "ref": byProvider["antigravity"].Ref},
		map[string]any{"source": "codex", "ref": byProvider["codex"].Ref},
	}}
	payload, _ := json.Marshal(picks)
	rec = httptest.NewRecorder()
	g.handleImportAppsSubmit(rec, httptest.NewRequest(http.MethodPost, "/api/importapps", strings.NewReader(string(payload))))
	if rec.Code != http.StatusOK {
		t.Fatalf("submit = %d %s", rec.Code, rec.Body.String())
	}
	if len(g.pool.ListByProvider("antigravity")) != 1 || len(g.pool.ListByProvider("codex")) != 1 {
		t.Fatalf("accounts = anti %d codex %d", len(g.pool.ListByProvider("antigravity")), len(g.pool.ListByProvider("codex")))
	}
	if got := g.pool.ListByProvider("antigravity")[0].GetCredential("access_token"); got != "anti-access" {
		t.Fatalf("antigravity token = %q", got)
	}
}

func TestLocalVaultWithoutKey(t *testing.T) {
	t.Setenv("AETHER_VAULT_DATA_DIR", t.TempDir())
	g := newAutoImportGateway(t)
	if entries := g.localVaultEntries(); len(entries) != 0 {
		t.Fatalf("missing key should yield no entries: %+v", entries)
	}
}
