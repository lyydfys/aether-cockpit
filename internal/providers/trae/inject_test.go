package trae

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// TestInjectLocalAccountUpdatesStorage 覆盖一键切号的核心契约：
// 只改 auth 条目，storage.json 其余键与 auth 里未知字段必须原样保留。
func TestInjectLocalAccountUpdatesStorage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("HOME", root)

	dir := filepath.Join(root, "Trae CN", "User", "globalStorage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "storage.json")
	auth := `{"token":"old","refreshToken":"oldr","host":"https://api.example","userId":"u1","expiredAt":1234567890123}`
	storage := map[string]any{
		authStorageKey:       auth,
		"telemetry.machineId": "machine-1",
		"window.state":        map[string]any{"x": 1.0},
	}
	raw, _ := json.Marshal(storage)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	p := newProvider("trae-cn", "CN")
	expiry := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	res, err := p.InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:  "acc-1",
		ProviderID: "trae-cn",
		Credentials: map[string]string{
			"token":         "new-token",
			"refresh_token": "new-refresh",
			"user_id":       "u1",
			"expires_at":    expiry.Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("InjectLocalAccount: %v", err)
	}
	if res.Target != "file:"+path {
		t.Fatalf("target = %q", res.Target)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	draft := p.parseStorageFile(path, after)
	if draft == nil {
		t.Fatal("写入后 storage.json 无法被本机导入解析")
	}
	if draft.Credentials["token"] != "new-token" || draft.Credentials["refresh_token"] != "new-refresh" {
		t.Fatalf("token 未更新: %+v", draft.Credentials)
	}
	if draft.Credentials["user_id"] != "u1" {
		t.Fatalf("user_id 丢失: %+v", draft.Credentials)
	}

	var store map[string]any
	if err := json.Unmarshal(after, &store); err != nil {
		t.Fatal(err)
	}
	if anyString(store["telemetry.machineId"]) != "machine-1" {
		t.Fatal("其他 storage 键被破坏")
	}
	authJSON, err := decodeAuthValue(anyString(store[authStorageKey]))
	if err != nil {
		t.Fatal(err)
	}
	var authMap map[string]any
	if err := json.Unmarshal(authJSON, &authMap); err != nil {
		t.Fatal(err)
	}
	if anyString(authMap["host"]) != "https://api.example" {
		t.Fatal("auth 里的未知字段 host 被丢弃")
	}
	if _, ok := authMap["expiredAt"].(float64); !ok {
		t.Fatalf("原有数字形态的 expiredAt 应保持数字: %T", authMap["expiredAt"])
	}
}

func TestInjectLocalAccountRequiresToken(t *testing.T) {
	p := newProvider("trae-cn", "CN")
	if _, err := p.InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:   "acc-2",
		ProviderID:  "trae-cn",
		Credentials: map[string]string{},
	}); err == nil {
		t.Fatal("缺 token 时应报错")
	}
}
