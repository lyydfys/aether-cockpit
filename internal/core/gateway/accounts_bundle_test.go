package gateway

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newBundleGateway(t *testing.T) *Gateway {
	t.Helper()
	dir := t.TempDir()
	return New(Config{
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		UsagePath:        "-",
	})
}

func exportBundle(t *testing.T, g *Gateway, pass string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"passphrase": pass})
	rec := httptest.NewRecorder()
	g.handleAccountsBundleExport(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/export/bundle", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAccountBundleRoundTrip(t *testing.T) {
	const pass = "correct-horse-battery"
	g := newBundleGateway(t)
	limit := 3
	env, apiErr := g.createAccount("mock-native", "Alice", map[string]string{
		"api_key": "sk-secret-123", "user_id": "u-bundle-1",
	}, "")
	if apiErr != nil {
		t.Fatal(apiErr.Message)
	}
	env.SetTags([]string{"主力", "备用"})
	env.SetNote("迁移备注")
	env.SetPriority(2)
	env.SetWeight(5)
	env.SetConcurrencyOverride(&limit)
	env.SetAllowedModels([]string{"model-a"})
	env.SetModelMapping(map[string]string{"claude-*": "model-a"})
	env.SetDisabled(true)

	out := exportBundle(t, g, pass)
	if n := int(out["accounts"].(float64)); n != 1 {
		t.Fatalf("accounts = %d", n)
	}
	data := out["data"].(string)
	if strings.Contains(data, "sk-secret-123") {
		t.Fatal("账号包泄漏了明文凭证")
	}
	if out["encrypted"] != true {
		t.Fatalf("encrypted = %v", out["encrypted"])
	}

	g2 := newBundleGateway(t)
	payload, _ := json.Marshal(map[string]any{"passphrase": pass, "data": data})
	rec := httptest.NewRecorder()
	g2.handleAccountsBundleImport(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/import/bundle", strings.NewReader(string(payload))))
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Imported []string            `json:"imported"`
		Skipped  []string            `json:"skipped"`
		Failed   []map[string]string `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 1 || len(res.Failed) != 0 {
		t.Fatalf("import result = %+v", res)
	}
	list := g2.pool.ListByProvider("mock-native")
	if len(list) != 1 {
		t.Fatalf("accounts = %d", len(list))
	}
	got := list[0]
	if got.GetCredential("api_key") != "sk-secret-123" || got.GetCredential("user_id") != "u-bundle-1" {
		t.Fatalf("credentials = %v", got.CredentialsSnapshot())
	}
	if tags := got.TagsList(); len(tags) != 2 || tags[0] != "主力" {
		t.Fatalf("tags = %v", tags)
	}
	if got.Note() != "迁移备注" || got.Priority() != 2 || got.Weight() != 5 {
		t.Fatalf("meta = note %q priority %d weight %d", got.Note(), got.Priority(), got.Weight())
	}
	if v, ok := got.ConcurrencyOverride(); !ok || v != 3 {
		t.Fatalf("concurrency = %d/%v", v, ok)
	}
	if models := got.AllowedModels(); len(models) != 1 || models[0] != "model-a" {
		t.Fatalf("allowed = %v", models)
	}
	if mapping := got.ModelMapping(); mapping["claude-*"] != "model-a" {
		t.Fatalf("mapping = %v", mapping)
	}
	if !got.Disabled() {
		t.Fatal("disabled not preserved")
	}

	// 再导入一次：同身份应跳过，不产生重复。
	rec = httptest.NewRecorder()
	g2.handleAccountsBundleImport(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/import/bundle", strings.NewReader(string(payload))))
	if rec.Code != http.StatusOK {
		t.Fatalf("second import = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Imported) != 0 || len(res.Skipped) != 1 {
		t.Fatalf("second import = %+v", res)
	}
	if n := len(g2.pool.ListByProvider("mock-native")); n != 1 {
		t.Fatalf("duplicate accounts = %d", n)
	}
}

func TestAccountBundleRejectsBadInput(t *testing.T) {
	const pass = "correct-horse-battery"
	g := newBundleGateway(t)

	body, _ := json.Marshal(map[string]any{"passphrase": "short"})
	rec := httptest.NewRecorder()
	g.handleAccountsBundleExport(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/export/bundle", strings.NewReader(string(body))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short passphrase = %d", rec.Code)
	}

	// 未加密的明文账号包必须拒绝。
	plain := base64.StdEncoding.EncodeToString([]byte(`{"format":"aether-accounts","version":1,"accounts":[]}`))
	payload, _ := json.Marshal(map[string]any{"passphrase": pass, "data": plain})
	rec = httptest.NewRecorder()
	g.handleAccountsBundleImport(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/import/bundle", strings.NewReader(string(payload))))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "明文") {
		t.Fatalf("plaintext = %d %s", rec.Code, rec.Body.String())
	}

	env, apiErr := g.createAccount("mock-native", "Alice", map[string]string{"api_key": "sk-1", "user_id": "u1"}, "")
	if apiErr != nil {
		t.Fatal(apiErr.Message)
	}
	_ = env
	out := exportBundle(t, g, pass)
	wrong, _ := json.Marshal(map[string]any{"passphrase": "wrong-password", "data": out["data"]})
	rec = httptest.NewRecorder()
	g.handleAccountsBundleImport(rec, httptest.NewRequest(http.MethodPost, "/api/accounts/import/bundle", strings.NewReader(string(wrong))))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "口令不对") {
		t.Fatalf("wrong pass = %d %s", rec.Code, rec.Body.String())
	}
}
