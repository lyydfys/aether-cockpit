package githubcopilot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/provider"
)

// TestInjectLocalAccountHosts 覆盖一键切号：只换 github.com 的 oauth_token，
// user 与其他主机条目保持不变。
func TestInjectLocalAccountHosts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("USERPROFILE", root)
	t.Setenv("HOME", root)
	t.Setenv("APPDATA", root)
	t.Setenv("LOCALAPPDATA", root)

	dir := filepath.Join(root, ".config", "github-copilot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hosts.json")
	raw := []byte(`{"github.com":{"user":"octo","oauth_token":"ghu_old"},"ghe.example":{"user":"corp","oauth_token":"ghu_corp"}}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Provider{}
	res, err := p.InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:   "acc-1",
		ProviderID:  channelID,
		Credentials: map[string]string{"github_token": "ghu_new"},
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
	var root2 map[string]map[string]any
	if err := json.Unmarshal(after, &root2); err != nil {
		t.Fatal(err)
	}
	if root2["github.com"]["oauth_token"] != "ghu_new" {
		t.Fatalf("github.com token 未更新: %+v", root2["github.com"])
	}
	if root2["github.com"]["user"] != "octo" {
		t.Fatal("user 字段被覆盖")
	}
	if root2["ghe.example"]["oauth_token"] != "ghu_corp" {
		t.Fatal("企业主机条目被破坏")
	}

	drafts := parseCopilotHosts(after)
	if len(drafts) != 2 {
		t.Fatalf("写入后应仍能解析出 2 个登录态: %+v", drafts)
	}
	found := false
	for _, d := range drafts {
		if d.Credentials["github_token"] == "ghu_new" {
			found = true
		}
	}
	if !found {
		t.Fatal("新 token 无法被本机导入解析")
	}
}

func TestInjectLocalAccountCreatesHostsFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("USERPROFILE", root)
	t.Setenv("HOME", root)
	t.Setenv("APPDATA", root)
	t.Setenv("LOCALAPPDATA", root)

	p := &Provider{}
	if _, err := p.InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:   "acc-2",
		ProviderID:  channelID,
		Credentials: map[string]string{"api_key": "ghu_fresh"},
	}); err != nil {
		t.Fatalf("InjectLocalAccount: %v", err)
	}
	path := filepath.Join(root, "github-copilot", "hosts.json")
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("应新建 APPDATA/github-copilot/hosts.json: %v", err)
	}
	if d := parseCopilotHosts(after); len(d) != 1 || d[0].Credentials["github_token"] != "ghu_fresh" {
		t.Fatalf("新建文件内容不可用: %+v", d)
	}
}
