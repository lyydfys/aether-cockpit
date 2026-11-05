package gateway

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// 升级兼容：新版数据目录不存在、但旧目录里有库时必须回退，
// 否则「本机配置库」列表在升级后静默变空（数据还在，用户以为丢了）。
func TestLocalSQLiteDBPathFallsBackToLegacyDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AETHER_LOCAL_SQLITE_DB", "")
	t.Setenv(legacyLocalSQLiteDBEnv(), "")

	legacy := legacyLocalSQLiteDBPath(home)
	if legacy == "" {
		t.Fatal("legacy path 不应为空")
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := localSQLiteDBPath(); got != legacy {
		t.Fatalf("应回退到旧路径 %q，实际 %q", legacy, got)
	}
}

// 新路径存在时必须优先用新路径（回退逻辑不能反过来劫持已迁移的用户）。
func TestLocalSQLiteDBPathPrefersCurrentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("AETHER_LOCAL_SQLITE_DB", "")
	t.Setenv(legacyLocalSQLiteDBEnv(), "")

	current := filepath.Join(home, ".local-provider-switch", "provider-switch.db")
	if err := os.MkdirAll(filepath.Dir(current), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := legacyLocalSQLiteDBPath(home)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := localSQLiteDBPath(); got != current {
		t.Fatalf("应优先新路径 %q，实际 %q", current, got)
	}
}

func seedLocalSQLiteDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "provider-switch.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE providers (id TEXT, app_type TEXT, name TEXT, settings_config TEXT)`); err != nil {
		t.Fatal(err)
	}
	rows := [][4]string{
		{"c1", "claude", "My Claude", `{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-relay-claude","ANTHROPIC_BASE_URL":"http://localhost:3000"}}`},
		{"x1", "codex", "xem", `{"auth":{"OPENAI_API_KEY":"sk-relay-codex"},"config":"model_provider = \"x\"\n[model_providers.x]\nbase_url = \"https://relay.example/v1\"\n"}`},
		{"g1", "gemini", "Google", `{"env":{},"config":{}}`},
	}
	for _, r := range rows {
		if _, err := db.Exec("INSERT INTO providers(id,app_type,name,settings_config) VALUES(?,?,?,?)", r[0], r[1], r[2], r[3]); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AETHER_LOCAL_SQLITE_DB", path)
	return path
}

func TestLocalSQLiteParse(t *testing.T) {
	seedLocalSQLiteDB(t)
	entries, err := readLocalSQLiteEntries()
	if err != nil {
		t.Fatalf("readLocalSQLiteEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d", len(entries))
	}
	byID := map[string]localSQLiteEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	if e := byID["c1"]; e.Target != "claude-sub" || e.Skip != "" || e.BaseURL != "http://localhost:3000" || e.APIKey != "sk-relay-claude" {
		t.Fatalf("claude 条目 = %+v", e)
	}
	if e := byID["x1"]; e.Target != "codex" || e.Skip != "" || e.BaseURL != "https://relay.example/v1" || e.APIKey != "sk-relay-codex" {
		t.Fatalf("codex 条目 = %+v", e)
	}
	if e := byID["g1"]; e.Skip == "" {
		t.Fatal("gemini 应标记为暂不支持")
	}
}

func TestLocalSQLiteImportFlow(t *testing.T) {
	seedLocalSQLiteDB(t)
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})

	rec := httptest.NewRecorder()
	g.handleImportApps(rec, httptest.NewRequest(http.MethodGet, "/api/importapps", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET importapps = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "localsqlite:c1") || !strings.Contains(body, "localsqlite:x1") {
		t.Fatalf("列表里没有 本机配置库 条目: %s", body)
	}
	if strings.Contains(body, "sk-relay-claude") || strings.Contains(body, "sk-relay-codex") {
		t.Fatal("列表接口泄露了明文 Key")
	}
	if strings.Contains(body, "localsqlite:g1") {
		t.Fatal("不支持的 gemini 条目不应出现")
	}

	submit := `{"picks":[{"source":"claude-sub","ref":"localsqlite:c1"},{"source":"codex","ref":"localsqlite:x1"}]}`
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/importapps", strings.NewReader(submit))
	g.handleImportAppsSubmit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST importapps = %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Added []string `json:"added"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Added) != 2 {
		t.Fatalf("added = %v", out.Added)
	}

	claudeAcc := g.pool.ListByProvider("claude-sub")
	if len(claudeAcc) != 1 {
		t.Fatalf("claude 账号 = %d", len(claudeAcc))
	}
	if claudeAcc[0].GetCredential("api_key") != "sk-relay-claude" ||
		claudeAcc[0].GetCredential("base_url") != "http://localhost:3000" {
		t.Fatal("claude 账号凭证不正确")
	}
	codexAcc := g.pool.ListByProvider("codex")
	if len(codexAcc) != 1 {
		t.Fatalf("codex 账号 = %d", len(codexAcc))
	}
	if codexAcc[0].GetCredential("access_token") != "sk-relay-codex" ||
		codexAcc[0].GetCredential("base_url") != "https://relay.example/v1" {
		t.Fatal("codex 账号凭证不正确")
	}

	// 再列一次：应标记为已导入。
	rec = httptest.NewRecorder()
	g.handleImportApps(rec, httptest.NewRequest(http.MethodGet, "/api/importapps", nil))
	if !strings.Contains(rec.Body.String(), "已导入过这个账号") {
		t.Fatal("重复导入未被标记")
	}
}
