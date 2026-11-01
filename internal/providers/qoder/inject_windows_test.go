//go:build windows

package qoder

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/provider"
)

// craftAuthFile 在中立目录里造一份与 Electron 相同格式的登录态。
func craftAuthFile(t *testing.T, prof profile, plain map[string]any) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	dir := filepath.Join(root, prof.appDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	protected, err := dpapiProtect(key)
	if err != nil {
		t.Fatalf("dpapiProtect: %v", err)
	}
	state, _ := json.Marshal(map[string]any{
		"os_crypt": map[string]any{
			"encrypted_key": base64.StdEncoding.EncodeToString(append([]byte("DPAPI"), protected...)),
		},
	})
	if err := os.WriteFile(filepath.Join(dir, "Local State"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plain)
	sealed, err := encryptV10(raw, key)
	if err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(dir, "auth.v1.dat")
	if err := os.WriteFile(authPath, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	return authPath
}

func TestInjectLocalAccountRewritesAuthFile(t *testing.T) {
	authPath := craftAuthFile(t, cnProfile, map[string]any{
		"token":        "old-token",
		"refreshToken": "old-refresh",
		"expiresAt":    "2026-10-01T00:00:00Z",
		"user":         map[string]any{"id": "u1", "name": "旧名字", "email": "old@example.com"},
		"unknownKey":   "keep-me",
	})
	p := newProvider(cnProfile)
	res, err := p.InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:  "acc-q1",
		ProviderID: cnProfile.id,
		Credentials: map[string]string{
			"token":         "new-token",
			"refresh_token": "new-refresh",
			"expires_at":    "2026-12-01T00:00:00Z",
			"user_name":     "新名字",
		},
	})
	if err != nil {
		t.Fatalf("InjectLocalAccount: %v", err)
	}
	if res.Target != "file:"+authPath || len(res.Files) != 1 {
		t.Fatalf("res = %+v", res)
	}

	_, plain, _, err := readDesktopAuthRaw(cnProfile)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["token"] != "new-token" || doc["refreshToken"] != "new-refresh" {
		t.Fatalf("token 未更新: %+v", doc)
	}
	user, _ := doc["user"].(map[string]any)
	if user["name"] != "新名字" || user["id"] != "u1" || user["email"] != "old@example.com" {
		t.Fatalf("user 未按预期合并: %+v", user)
	}
	if doc["unknownKey"] != "keep-me" {
		t.Fatal("未知字段被丢弃")
	}

	// 备份是写入前的内容（旧 token）。
	backup, err := os.ReadFile(res.Files[0])
	if err != nil {
		t.Fatal(err)
	}
	key, err := chromiumKey(filepath.Join(filepath.Dir(authPath), "Local State"))
	if err != nil {
		t.Fatal(err)
	}
	oldPlain, err := decryptV10(backup, key)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(oldPlain) || !containsToken(oldPlain, "old-token") {
		t.Fatal("备份不是写入前状态")
	}

	if !p.Manifest().Capabilities.LocalInject {
		t.Fatal("Manifest 应声明 local_inject")
	}
}

func containsToken(raw []byte, want string) bool {
	var doc struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	return doc.Token == want
}

func TestInjectLocalAccountMissingAuth(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	if _, err := newProvider(intlProfile).InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:   "acc-q2",
		ProviderID:  intlProfile.id,
		Credentials: map[string]string{"token": "t"},
	}); err == nil {
		t.Fatal("没有登录态时应报错")
	}
}
