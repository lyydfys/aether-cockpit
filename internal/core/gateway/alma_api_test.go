package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	_ "aether-cockpit/internal/providers/openaicompat"
)

// TestAlmaImportFlow 用假 Alma 服务覆盖：列表（含跳过原因、不泄露 Key）、提交入库、未运行静默。
func TestAlmaImportFlow(t *testing.T) {
	alma := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/providers" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "p1", "name": "我的中转", "type": "openai", "baseURL": "https://relay.example/v1", "apiKey": "sk-alma", "enabled": true},
			{"id": "p2", "name": "关掉的", "type": "openai", "baseURL": "https://x/v1", "apiKey": "k", "enabled": false},
			{"id": "p3", "name": "自指向", "type": "openai", "baseURL": "http://127.0.0.1:7860/v1", "apiKey": "k", "enabled": true},
			{"id": "p4", "name": "Anthropic", "type": "anthropic", "baseURL": "https://a/v1", "apiKey": "k", "enabled": true},
		})
	}))
	defer alma.Close()
	t.Setenv("ALMA_API", alma.URL)

	g := New(Config{
		Addr:             "127.0.0.1:7860",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})

	rec := httptest.NewRecorder()
	g.handleAlmaImportList(rec, httptest.NewRequest(http.MethodGet, "/api/import/alma", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "sk-alma") {
		t.Fatal("列表泄露了 Key")
	}
	var list struct {
		Available bool `json:"available"`
		Entries   []struct {
			ID   string `json:"id"`
			Skip string `json:"skip"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if !list.Available || len(list.Entries) != 4 {
		t.Fatalf("entries = %+v", list)
	}
	skips := map[string]string{}
	for _, e := range list.Entries {
		skips[e.ID] = e.Skip
	}
	if skips["p1"] != "" || skips["p2"] == "" || skips["p3"] == "" || skips["p4"] == "" {
		t.Fatalf("跳过原因 = %+v", skips)
	}

	// 只导 p1。
	rec = httptest.NewRecorder()
	g.handleAlmaImport(rec, httptest.NewRequest(http.MethodPost, "/api/import/alma", strings.NewReader(`{"ids":["p1"]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Added []string `json:"added"`
		Count int      `json:"count"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Count != 1 || out.Added[0] != "Alma · 我的中转" {
		t.Fatalf("out = %+v", out)
	}
	accs := g.pool.ListByProvider("openai-compat")
	if len(accs) != 1 || accs[0].GetCredential("api_key") != "sk-alma" ||
		accs[0].GetCredential("base_url") != "https://relay.example/v1" {
		t.Fatalf("账号 = %+v", accs)
	}
}

func TestAlmaNotRunningIsSilent(t *testing.T) {
	t.Setenv("ALMA_API", "http://127.0.0.1:1")
	g := New(Config{Addr: "127.0.0.1:7860", UsagePath: "-"})
	rec := httptest.NewRecorder()
	g.handleAlmaImportList(rec, httptest.NewRequest(http.MethodGet, "/api/import/alma", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"available":false`) {
		t.Fatalf("未运行时 = %d %s", rec.Code, rec.Body.String())
	}
}
