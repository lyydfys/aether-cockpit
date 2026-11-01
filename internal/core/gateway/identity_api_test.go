package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestClassifyCodexIdentity(t *testing.T) {
	cases := []struct {
		ua, originator, session string
		official                bool
		mismatches              int
	}{
		{"codex_cli_rs/0.48.0", "codex_cli_rs", "sess-1", true, 0},
		{"Codex_CLI_RS/1.0", "codex_cli_rs", "s", true, 0},
		{"curl/8.5", "codex_cli_rs", "s", false, 1},
		{"codex_cli_rs/0.48.0", "my-agent", "s", false, 1},
		{"codex_cli_rs/0.48.0", "codex_cli_rs", "", false, 1},
		{"", "", "", false, 3},
	}
	for _, c := range cases {
		official, mism := classifyCodexIdentity(c.ua, c.originator, c.session)
		if official != c.official || len(mism) != c.mismatches {
			t.Errorf("classify(%q,%q,%q) = %v %v", c.ua, c.originator, c.session, official, mism)
		}
	}
}

func TestCodexIdentityEndpoint(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	// 两条：官方形状与第三方形状。
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("User-Agent", "codex_cli_rs/0.48.0")
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("session_id", "sess-1")
	g.recordCodexIdentity(req)

	req = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("User-Agent", "some-relay/1.0")
	g.recordCodexIdentity(req)

	rec := httptest.NewRecorder()
	g.handleCodexIdentity(rec, httptest.NewRequest(http.MethodGet, "/api/providers/codex/identity?limit=5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out struct {
		Expected map[string]any   `json:"expected"`
		Items    []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("items = %d", len(out.Items))
	}
	// 最近的在前：第三方那条。
	if out.Items[0]["official_like"] != false || len(out.Items[0]["mismatches"].([]any)) == 0 {
		t.Fatalf("最新一条 = %+v", out.Items[0])
	}
	if out.Items[1]["official_like"] != true {
		t.Fatalf("官方形状那条 = %+v", out.Items[1])
	}
	if out.Expected["originator"] != "codex_cli_rs" {
		t.Fatalf("expected = %+v", out.Expected)
	}
}
