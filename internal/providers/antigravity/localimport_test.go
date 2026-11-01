package antigravity

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLocalCredentialVariants(t *testing.T) {
	payload := map[string]any{
		"token": map[string]any{
			"access_token":  "ya29.token",
			"refresh_token": "1//refresh",
			"token_type":    "Bearer",
			"expiry":        "2026-10-28T00:00:00Z",
		},
		"auth_method": "consumer",
	}
	raw, _ := json.Marshal(payload)
	d := parseLocalCredential(raw)
	if d == nil || d.Credentials["access_token"] != "ya29.token" || d.Credentials["refresh_token"] != "1//refresh" {
		t.Fatalf("plain = %+v", d)
	}
	if d.ExpiresAt == 0 {
		t.Fatal("expiry 未解析")
	}

	// go-keyring-base64: 前缀（macOS/Linux Keyring 形态）。
	wrapped := append([]byte("go-keyring-base64:"), []byte(base64.StdEncoding.EncodeToString(raw))...)
	if d := parseLocalCredential(wrapped); d == nil || d.Credentials["refresh_token"] != "1//refresh" {
		t.Fatalf("keyring-base64 = %+v", d)
	}

	// 缺 access_token 但仅 refresh_token：仍可导入（首次请求会刷新）。
	onlyRefresh, _ := json.Marshal(map[string]any{"token": map[string]any{"refresh_token": "1//only"}})
	if d := parseLocalCredential(onlyRefresh); d != nil {
		t.Fatalf("当前规则要求 access_token，得到 %+v", d)
	}
}

func TestParseLocalCredentialEmailLabel(t *testing.T) {
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"dev@example.com"}`))
	idToken := "eyJhbGciOiJub25lIn0." + claims + ".sig"
	raw, _ := json.Marshal(map[string]any{
		"token":    map[string]any{"access_token": "tok", "refresh_token": "r"},
		"id_token": idToken,
	})
	d := parseLocalCredential(raw)
	if d == nil || !strings.Contains(d.Label, "dev@example.com") {
		t.Fatalf("label = %+v", d)
	}
}

func TestReadFallbackTokenFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gemini")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "jetski-standalone-oauth-token")
	if err := os.WriteFile(path, []byte(`{"token":{"access_token":"a","refresh_token":"r"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := readFallbackTokenFile()
	if err != nil || len(raw) == 0 {
		t.Fatalf("readFallbackTokenFile = %q, %v", raw, err)
	}
	if d := parseLocalCredential(raw); d == nil {
		t.Fatal("回退文件应可解析")
	}
}
