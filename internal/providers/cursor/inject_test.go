package cursor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestInjectLocalAccountWritesStateDB(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APPDATA", root)
	dir := filepath.Join(root, "Cursor", "User", "globalStorage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.vscdb")
	seed, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`CREATE TABLE ItemTable (key TEXT UNIQUE ON CONFLICT REPLACE, value BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`INSERT INTO ItemTable(key,value) VALUES
		('workbench.colorTheme','dark'),
		('cursorAuth/accessToken','old-access'),
		('cursorAuth/cachedEmail','old@example.com')`); err != nil {
		t.Fatal(err)
	}
	seed.Close()

	res, err := (&Provider{}).InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:  "acc-1",
		ProviderID: channelID,
		Credentials: map[string]string{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"email":         "new@example.com",
			"membership":    "pro",
		},
	})
	if err != nil {
		t.Fatalf("InjectLocalAccount: %v", err)
	}
	if res.Target != "sqlite:"+path || len(res.Files) != 1 {
		t.Fatalf("res = %+v", res)
	}

	draft, err := readCursorAuth(path)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if draft.Credentials["access_token"] != "new-access" || draft.Credentials["refresh_token"] != "new-refresh" {
		t.Fatalf("token 未写回: %+v", draft.Credentials)
	}
	if draft.Credentials["email"] != "new@example.com" || draft.Credentials["membership"] != "pro" {
		t.Fatalf("账号信息未写回: %+v", draft.Credentials)
	}

	check, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var theme string
	if err := check.QueryRow("SELECT value FROM ItemTable WHERE key='workbench.colorTheme'").Scan(&theme); err != nil || theme != "dark" {
		t.Fatalf("无关键被破坏: %q %v", theme, err)
	}

	backup, err := sql.Open("sqlite", "file:"+filepath.ToSlash(res.Files[0])+"?mode=ro")
	if err != nil {
		t.Fatalf("备份打不开: %v", err)
	}
	defer backup.Close()
	var old string
	if err := backup.QueryRow("SELECT value FROM ItemTable WHERE key='cursorAuth/accessToken'").Scan(&old); err != nil || old != "old-access" {
		t.Fatalf("备份不是写入前状态: %q %v", old, err)
	}
}

func TestInjectLocalAccountMissingDB(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	drafts, err := (&Provider{}).DiscoverLocalAccounts(context.Background())
	if err == nil || drafts != nil {
		t.Fatal("无数据库时应返回可执行错误")
	}
	if _, err := (&Provider{}).InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:   "acc-2",
		ProviderID:  channelID,
		Credentials: map[string]string{"access_token": "x"},
	}); err == nil {
		t.Fatal("无数据库时应报错")
	}
	if !(&Provider{}).Manifest().Capabilities.LocalInject {
		t.Fatal("Cursor Manifest 应声明 local_inject")
	}
}
