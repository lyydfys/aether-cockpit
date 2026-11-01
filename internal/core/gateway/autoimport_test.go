package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func newAutoImportGateway(t *testing.T) *Gateway {
	t.Helper()
	dir := t.TempDir()
	return New(Config{
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		UsagePath:        "-",
	})
}

func fakeDrafts(list ...*provider.CredentialDraft) func(context.Context) map[string][]*provider.CredentialDraft {
	return func(context.Context) map[string][]*provider.CredentialDraft {
		return map[string][]*provider.CredentialDraft{"mock-native": list}
	}
}

func draft(label, userID, key string) *provider.CredentialDraft {
	return &provider.CredentialDraft{
		Label:       label,
		Credentials: map[string]string{"user_id": userID, "api_key": key},
	}
}

func TestAutoImportDedupAndTombstone(t *testing.T) {
	g := newAutoImportGateway(t)
	g.autoImp.scan = fakeDrafts(draft("Alice", "u1", "k1"))

	st := g.scanAutoImport(context.Background())
	if len(st.LastAdded) != 1 || st.LastAdded[0] != "Alice" {
		t.Fatalf("first scan = %+v", st)
	}
	if st.Running || st.LastScan == 0 || st.Seen < 1 {
		t.Fatalf("scan result not finalized: %+v", st)
	}
	if n := len(g.pool.ListByProvider("mock-native")); n != 1 {
		t.Fatalf("accounts = %d, want 1", n)
	}

	// 同一身份（token 换了）不能再导一次。
	g.autoImp.scan = fakeDrafts(&provider.CredentialDraft{
		Label: "Alice", Credentials: map[string]string{"user_id": "u1", "api_key": "k1-rotated"},
	})
	st = g.scanAutoImport(context.Background())
	if len(st.LastAdded) != 0 {
		t.Fatalf("second scan imported: %+v", st)
	}
	if n := len(g.pool.ListByProvider("mock-native")); n != 1 {
		t.Fatalf("accounts after rotate = %d, want 1", n)
	}

	// 用户删掉账号后，自动导入不能把它复活。
	env := g.pool.ListByProvider("mock-native")[0]
	if !g.pool.Remove(env.AccountID) {
		t.Fatal("remove failed")
	}
	st = g.scanAutoImport(context.Background())
	if len(st.LastAdded) != 0 || len(g.pool.ListByProvider("mock-native")) != 0 {
		t.Fatalf("deleted account was re-imported: %+v", st)
	}

	// 清除已见记录后，下一次扫描允许重新导入。
	reset := httptest.NewRecorder()
	g.handleAutoImportReset(reset, httptest.NewRequest(http.MethodPost, "/api/importapps/auto/reset", nil))
	st = g.scanAutoImport(context.Background())
	if len(st.LastAdded) != 1 {
		t.Fatalf("after reset = %+v", st)
	}
}

func TestAutoImportSkipsExistingIdentityAndUniquesLabels(t *testing.T) {
	g := newAutoImportGateway(t)
	if _, apiErr := g.createAccount("mock-native", "Manual", map[string]string{"user_id": "u2", "api_key": "manual"}, ""); apiErr != nil {
		t.Fatal(apiErr.Message)
	}
	g.autoImp.scan = fakeDrafts(
		draft("Bob", "u2", "local-token"), // 与手动账号同一身份
		draft("Bob", "u3", "k3"),          // 同名但不同身份
		draft("Bob", "u4", "k4"),
	)
	st := g.scanAutoImport(context.Background())
	if len(st.LastAdded) != 2 {
		t.Fatalf("added = %+v", st.LastAdded)
	}
	labels := map[string]bool{}
	for _, e := range g.pool.ListByProvider("mock-native") {
		labels[e.Label] = true
	}
	if !labels["Manual"] || !labels["Bob"] || !labels["Bob #2"] {
		t.Fatalf("labels = %v", labels)
	}
}

func TestAutoImportStatePersists(t *testing.T) {
	g := newAutoImportGateway(t)
	g.autoImp.scan = fakeDrafts(draft("Alice", "u1", "k1"))
	g.scanAutoImport(context.Background())

	g2 := New(Config{
		SettingsPath:     g.settingsPath,
		AccountStorePath: filepath.Join(filepath.Dir(g.settingsPath), "accounts.json"),
		UsagePath:        "-",
	})
	g2.autoImp.scan = fakeDrafts(&provider.CredentialDraft{
		Label: "Alice", Credentials: map[string]string{"user_id": "u1", "api_key": "k1-rotated"},
	})
	st := g2.scanAutoImport(context.Background())
	if len(st.LastAdded) != 0 {
		t.Fatalf("seen state not persisted: %+v", st)
	}
	if st.Seen < 1 {
		t.Fatalf("seen = %d", st.Seen)
	}

	// 原账号文件也已落盘：新实例不应再出现同身份账号。
	for _, e := range g2.pool.ListByProvider("mock-native") {
		if envLocalIdentity(e) != localDraftIdentity("mock-native", draft("Alice", "u1", "x")) {
			t.Fatalf("unexpected account %+v", e.Snapshot())
		}
	}
	if n := len(g2.pool.ListByProvider("mock-native")); n > 1 {
		t.Fatalf("duplicate accounts = %d", n)
	}
}

func TestAutoImportAPI(t *testing.T) {
	g := newAutoImportGateway(t)
	g.autoImp.scan = fakeDrafts()

	rec := httptest.NewRecorder()
	g.handleAutoImportToggle(rec, httptest.NewRequest(http.MethodPost, "/api/importapps/auto", strings.NewReader(`{"enabled":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("toggle = %d %s", rec.Code, rec.Body.String())
	}
	if !g.autoImportEnabled() {
		t.Fatal("enabled flag not saved")
	}
	rec = httptest.NewRecorder()
	g.handleAutoImportStatus(rec, httptest.NewRequest(http.MethodGet, "/api/importapps/auto", nil))
	var st autoImportStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Enabled {
		t.Fatalf("status = %+v", st)
	}

	rec = httptest.NewRecorder()
	g.handleAutoImportToggle(rec, httptest.NewRequest(http.MethodPost, "/api/importapps/auto", strings.NewReader(`{"enabled":false}`)))
	if g.autoImportEnabled() {
		t.Fatal("disable failed")
	}
}
