package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestManualRefreshPersists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"at-new","refresh_token":"rt-new","expires_in":3600}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := Config{
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		UsagePath:        "-",
	}
	g := New(cfg)
	env, apiErr := g.createAccount("minimax-code", "ref", map[string]string{
		"access_token": "old", "refresh_token": "rt-old", "account_base_url": srv.URL,
	}, "")
	if apiErr != nil {
		t.Fatal(apiErr.Message)
	}
	env.SetHTTPClient(srv.Client())
	g.saveAccountsNow()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/accounts/{id}/refresh", g.handleTokenRefresh)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/"+env.AccountID+"/refresh", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d %s", rec.Code, rec.Body.String())
	}

	// 新实例从同一份 accounts.json 恢复：刷新后的 token 必须还在（加密落盘）。
	g2 := New(cfg)
	restored, ok := g2.pool.Get(env.AccountID)
	if !ok {
		t.Fatal("账号未从磁盘恢复")
	}
	if got := restored.GetCredential("access_token"); got != "at-new" {
		t.Fatalf("恢复的 access_token = %q", got)
	}
	if got := restored.GetCredential("refresh_token"); got != "rt-new" {
		t.Fatalf("恢复的 refresh_token = %q", got)
	}
}

// 积分渠道没有 token 续期能力；刷新按钮仍必须把额度读回来。
func TestAccountRefreshReadsQuotaWithoutTokenRefresh(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		UsagePath:        "-",
	})
	env, apiErr := g.createAccount("mock-native", "quota-refresh", map[string]string{"api_key": "k"}, "")
	if apiErr != nil {
		t.Fatal(apiErr.Message)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+env.AccountID+"/refresh", nil)
	req.SetPathValue("id", env.AccountID)
	g.handleTokenRefresh(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Refreshed  bool                  `json:"refreshed"`
		QuotaRead  bool                  `json:"quota_read"`
		Quota      *provider.QuotaStatus `json:"quota"`
		QuotaError string                `json:"quota_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Refreshed {
		t.Fatal("该渠道没有续期能力，不应声称刷新了凭证")
	}
	if !out.QuotaRead || out.QuotaError != "" {
		t.Fatalf("额度应读取成功: %+v", out)
	}
	if out.Quota == nil || out.Quota.Total != 10000 || out.Quota.ReadAt == 0 {
		t.Fatalf("quota = %+v", out.Quota)
	}
}

func TestManualRefreshWithoutQuotaDoesNotReportQuotaError(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		UsagePath:        "-",
	})
	env, apiErr := g.createAccount("windsurf", "refresh-only", map[string]string{}, "")
	if apiErr != nil {
		t.Fatal(apiErr.Message)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+env.AccountID+"/refresh", nil)
	req.SetPathValue("id", env.AccountID)
	g.handleTokenRefresh(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Refreshed        bool   `json:"refreshed"`
		RefreshSupported bool   `json:"refresh_supported"`
		QuotaSupported   bool   `json:"quota_supported"`
		QuotaRead        bool   `json:"quota_read"`
		QuotaError       string `json:"quota_error"`
		Warning          string `json:"warning"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.RefreshSupported || out.QuotaSupported || out.QuotaRead || out.QuotaError != "" {
		t.Fatalf("refresh-only result = %+v", out)
	}
	if out.Refreshed || out.Warning == "" {
		t.Fatalf("missing auth1_token should report a credential refresh warning: %+v", out)
	}
}

func TestAccountTagsPersistImmediately(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		UsagePath:        "-",
	}
	g := New(cfg)
	env, apiErr := g.createAccount("mock-native", "tagged", map[string]string{"api_key": "k"}, "")
	if apiErr != nil {
		t.Fatal(apiErr.Message)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/accounts/{id}/tags", g.handleSetAccountTags)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+env.AccountID+"/tags", strings.NewReader(`{"tags":["主力","临期"]}`))
	req.SetPathValue("id", env.AccountID)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tags = %d %s", rec.Code, rec.Body.String())
	}

	g2 := New(cfg)
	restored, ok := g2.pool.Get(env.AccountID)
	if !ok {
		t.Fatal("账号未恢复")
	}
	if tags := restored.TagsList(); len(tags) != 2 || tags[0] != "主力" {
		t.Fatalf("tags = %#v", tags)
	}
}

func TestAccountAddDeletePersistImmediately(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		UsagePath:        "-",
	}
	g := New(cfg)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/accounts", g.handleAddAccount)
	mux.HandleFunc("DELETE /api/accounts/{id}", g.handleDeleteAccount)
	rec := httptest.NewRecorder()
	body := `{"provider_id":"mock-native","label":"added","credentials":{"api_key":"k"}}`
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/accounts", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("add = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Account struct {
			AccountID string `json:"account_id"`
		} `json:"account"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	id := out.Account.AccountID
	if id == "" {
		t.Fatal("缺少 account_id")
	}

	g2 := New(cfg)
	if _, ok := g2.pool.Get(id); !ok {
		t.Fatal("新增账号未立即落盘")
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/accounts/"+id, nil)
	req.SetPathValue("id", id)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	g3 := New(cfg)
	if _, ok := g3.pool.Get(id); ok {
		t.Fatal("删除的账号在重启后复活")
	}
}

func TestAccountNoteAndCreatedAtPersist(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		UsagePath:        "-",
	}
	g := New(cfg)
	env, apiErr := g.createAccount("mock-native", "noted", map[string]string{"api_key": "k"}, "")
	if apiErr != nil {
		t.Fatal(apiErr.Message)
	}
	first := env.Snapshot().CreatedAt
	if first <= 0 {
		t.Fatalf("created_at 未记录: %+v", env.Snapshot())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/accounts/{id}/note", g.handleSetAccountNote)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+env.AccountID+"/note", strings.NewReader(`{"note":"  今天开始限流  "}`))
	req.SetPathValue("id", env.AccountID)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("note = %d %s", rec.Code, rec.Body.String())
	}
	if got := env.Note(); got != "今天开始限流" {
		t.Fatalf("note = %q", got)
	}

	// 再保存一次（标签变化），创建时间不能被改写
	env.SetTags([]string{"x"})
	g.saveAccountsNow()

	g2 := New(cfg)
	restored, ok := g2.pool.Get(env.AccountID)
	if !ok {
		t.Fatal("账号未恢复")
	}
	if got := restored.Note(); got != "今天开始限流" {
		t.Fatalf("note 未恢复: %q", got)
	}
	if got := restored.Snapshot().CreatedAt; got != first {
		t.Fatalf("created_at 变了: %d -> %d", first, got)
	}
}
