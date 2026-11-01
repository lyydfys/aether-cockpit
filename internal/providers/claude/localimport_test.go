package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

// fixtures 全部是本地构造的样例字符串，绝不读取真实凭证文件。
// 值都使用明显的占位符，避免测试输出里出现任何真实凭证。
const (
	fixtureOfficial = `{
	  "claudeAiOauth": {
	    "accessToken": "sk-ant-oat01-example-access",
	    "refreshToken": "sk-ant-ort01-example-refresh",
	    "expiresAt": 1893456000000,
	    "scopes": ["user:inference", "user:profile"]
	  }
	}`
	fixtureWithClientID = `{
	  "claudeAiOauth": {
	    "accessToken": "sk-ant-oat01-example-access",
	    "refreshToken": "sk-ant-ort01-example-refresh",
	    "expiresAt": 1893456000000,
	    "scopes": ["user:inference"],
	    "client_id": "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	  }
	}`
	fixtureSnakeCase = `{
	  "claudeAiOauth": {
	    "access_token": "sk-ant-oat01-example-access",
	    "refresh_token": "sk-ant-ort01-example-refresh",
	    "expires_at": "2030-01-02T03:04:05Z"
	  }
	}`
	fixtureTopLevel = `{
	  "accessToken": "sk-ant-oat01-example-access",
	  "refreshToken": "sk-ant-ort01-example-refresh",
	  "expiresAt": 1893456000000
	}`
)

// TestParseCredentialsFileOfficial 官方 camelCase 结构（字段名以公开资料与
// Claude Code 抓包为准：claudeAiOauth.accessToken / refreshToken / expiresAt 毫秒）。
func TestParseCredentialsFileOfficial(t *testing.T) {
	draft := parseCredentialsFile([]byte(fixtureOfficial))
	if draft == nil {
		t.Fatal("解析失败")
	}
	if draft.Credentials["access_token"] != "sk-ant-oat01-example-access" {
		t.Fatalf("access_token = %q", draft.Credentials["access_token"])
	}
	if draft.Credentials["refresh_token"] != "sk-ant-ort01-example-refresh" {
		t.Fatalf("refresh_token = %q", draft.Credentials["refresh_token"])
	}
	// expiresAt 毫秒时间戳应转成 RFC3339 且填充 draft.ExpiresAt。
	// 1893456000 秒 = 2030-01-01T00:00:00Z，格式化结果随本地时区带偏移。
	if want := time.Unix(1893456000, 0).Format(time.RFC3339); draft.Credentials["expires_at"] != want {
		t.Fatalf("expires_at = %q, want %q", draft.Credentials["expires_at"], want)
	}
	if draft.ExpiresAt != 1893456000 {
		t.Fatalf("ExpiresAt = %d", draft.ExpiresAt)
	}
	if _, has := draft.Credentials["oauth_client_id"]; has {
		t.Fatal("官方文件不带 client_id，不应出现 oauth_client_id")
	}
	if draft.Label == "" {
		t.Fatal("Label 为空")
	}
}

// TestParseCredentialsFileWithClientID 文件里带了 client_id 时必须带给刷新逻辑。
func TestParseCredentialsFileWithClientID(t *testing.T) {
	draft := parseCredentialsFile([]byte(fixtureWithClientID))
	if draft == nil {
		t.Fatal("解析失败")
	}
	if got := draft.Credentials["oauth_client_id"]; got != defaultClientID {
		t.Fatalf("oauth_client_id = %q", got)
	}
}

// TestParseCredentialsFileSnakeCase snake_case 兼容形态。
func TestParseCredentialsFileSnakeCase(t *testing.T) {
	draft := parseCredentialsFile([]byte(fixtureSnakeCase))
	if draft == nil {
		t.Fatal("snake_case 形态应可解析")
	}
	if draft.Credentials["access_token"] != "sk-ant-oat01-example-access" {
		t.Fatalf("credentials = %+v", draft.Credentials)
	}
	if draft.Credentials["expires_at"] == "" {
		t.Fatal("expires_at 未解析")
	}
}

// TestParseCredentialsFileTopLevel 顶层就是 oauth 对象的容错形态。
func TestParseCredentialsFileTopLevel(t *testing.T) {
	draft := parseCredentialsFile([]byte(fixtureTopLevel))
	if draft == nil {
		t.Fatal("顶层 oauth 对象应可解析")
	}
	if draft.Credentials["access_token"] == "" {
		t.Fatalf("credentials = %+v", draft.Credentials)
	}
}

// TestParseCredentialsFileInvalid 无 token / 坏 JSON / 陌生结构一律返回 nil。
func TestParseCredentialsFileInvalid(t *testing.T) {
	cases := map[string]string{
		"空 accessToken": `{"claudeAiOauth":{"accessToken":"","refreshToken":"x"}}`,
		"没有 oauth 对象":   `{"mcpOAuth":{"server":{"accessToken":"x"}}}`,
		"坏 JSON":        `{"claudeAiOauth":`,
		"oauth 不是对象":    `{"claudeAiOauth":"legacy-string"}`,
		"只有 scopes":     `{"claudeAiOauth":{"scopes":["user:inference"]}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if draft := parseCredentialsFile([]byte(raw)); draft != nil {
				t.Fatalf("应返回 nil, got %+v", draft)
			}
		})
	}
}

// TestDiscoverLocalAccountsFromFile 通过 CLAUDE_CONFIG_DIR 指到临时目录，
// 验证完整的"读文件 -> 出草稿"链路。
func TestDiscoverLocalAccountsFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(path, []byte(fixtureOfficial), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	p := &Provider{}
	drafts, err := p.DiscoverLocalAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 {
		t.Fatalf("drafts = %d", len(drafts))
	}
	d := drafts[0]
	if d.Credentials["access_token"] != "sk-ant-oat01-example-access" {
		t.Fatalf("draft = %+v", d)
	}
	if !strings.HasPrefix(d.Source, "local_import:") || !strings.Contains(d.Source, ".credentials.json") {
		t.Fatalf("source = %q", d.Source)
	}
}

// TestDiscoverLocalAccountsMissing 文件缺失时返回可执行的提示（NoAccount）。
func TestDiscoverLocalAccountsMissing(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	p := &Provider{}
	_, err := p.DiscoverLocalAccounts(context.Background())
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeNoAccount {
		t.Fatalf("err = %v", err)
	}
	// 提示必须可执行：告知登录或手动填写，且不泄露任何内容。
	if !strings.Contains(ae.Message, "/login") || !strings.Contains(ae.Message, "手动") {
		t.Fatalf("message = %q", ae.Message)
	}
}

// TestDiscoverLocalAccountsUnrecognized 结构不符时也要给出可执行提示。
func TestDiscoverLocalAccountsUnrecognized(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(path, []byte(`{"something":"else"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	p := &Provider{}
	_, err := p.DiscoverLocalAccounts(context.Background())
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeNoAccount {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(ae.Message, "claudeAiOauth") {
		t.Fatalf("message = %q", ae.Message)
	}
}

// TestTopLevelKeys 诊断辅助只吐键名。
func TestTopLevelKeys(t *testing.T) {
	keys := topLevelKeys([]byte(fixtureOfficial))
	if len(keys) != 1 || keys[0] != "claudeAiOauth" {
		t.Fatalf("keys = %v", keys)
	}
	if topLevelKeys([]byte(`not-json`)) != nil {
		t.Fatal("坏 JSON 应返回 nil")
	}
}
