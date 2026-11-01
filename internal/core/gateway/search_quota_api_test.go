package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/tools"
)

func TestReadTavilyUsage(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want SearchKeyQuota
		err  bool
	}{
		{
			name: "key limit",
			raw:  `{"key":{"usage":10,"limit":100},"account":{"current_plan":"Free","plan_limit":null}}`,
			want: SearchKeyQuota{Known: true, Used: 10, Limit: 100, Of: "key", Plan: "Free"},
		},
		{
			name: "plan limit with paygo",
			raw:  `{"key":{"usage":500,"limit":null},"account":{"current_plan":"Pro","plan_limit":1000,"plan_usage":200,"paygo_limit":100,"paygo_usage":20}}`,
			want: SearchKeyQuota{Known: true, Used: 200, Limit: 1000, Of: "plan", Plan: "Pro", PaygoLeft: 80},
		},
		{
			name: "unlimited",
			raw:  `{"key":{"usage":7,"limit":null},"account":{"current_plan":"Free","plan_limit":null}}`,
			want: SearchKeyQuota{Known: true, Unlimited: true, Used: 7, Plan: "Free"},
		},
		{
			name: "used up",
			raw:  `{"key":{"usage":100,"limit":100},"account":{"plan_limit":null}}`,
			want: SearchKeyQuota{Known: true, Used: 100, Limit: 100, Of: "key", UsedUp: true},
		},
		{name: "no key", raw: `{"account":{}}`, err: true},
		{name: "bad json", raw: `{`, err: true},
	}
	for _, c := range cases {
		got, err := readTavilyUsage([]byte(c.raw))
		if c.err {
			if err == nil {
				t.Fatalf("%s: 期望错误", c.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestSearchQuotaHandler(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/usage" {
			http.NotFound(w, r)
			return
		}
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":{"usage":10,"limit":100},"account":{"current_plan":"Free","plan_limit":null}}`))
	}))
	defer srv.Close()

	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.settings.Tools = &ToolsConfig{WebSearch: &tools.SearchConfig{
		Provider: "tavily", Endpoint: srv.URL + "/search", APIKey: "tvly-secret-key",
	}}

	req := httptest.NewRequest(http.MethodPost, "/api/settings/search-quota",
		strings.NewReader(`{"vendor":"tavily","fresh":true}`))
	rec := httptest.NewRecorder()
	g.handleSearchQuota(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Keys []SearchKeyQuota `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析: %v", err)
	}
	if len(out.Keys) != 1 || !out.Keys[0].Known || out.Keys[0].Used != 10 || out.Keys[0].Limit != 100 {
		t.Fatalf("keys = %+v", out.Keys)
	}
	if seenAuth != "Bearer tvly-secret-key" {
		t.Fatalf("auth = %q", seenAuth)
	}
	if out.Keys[0].Key == "tvly-secret-key" || !strings.Contains(out.Keys[0].Key, "…") {
		t.Fatalf("Key 未打码: %q", out.Keys[0].Key)
	}

	// 供应商不匹配：明确报错。
	req = httptest.NewRequest(http.MethodPost, "/api/settings/search-quota",
		strings.NewReader(`{"vendor":"serper"}`))
	rec = httptest.NewRecorder()
	g.handleSearchQuota(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("mismatch status = %d", rec.Code)
	}

	// 配置的是 Serper：没有用量接口，回空列表。
	g.settings.Tools.WebSearch.Provider = "serper"
	req = httptest.NewRequest(http.MethodPost, "/api/settings/search-quota",
		strings.NewReader(`{"vendor":"serper"}`))
	rec = httptest.NewRecorder()
	g.handleSearchQuota(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"keys":[]`) {
		t.Fatalf("serper = %d %s", rec.Code, rec.Body.String())
	}
}

func TestTavilyUsageURL(t *testing.T) {
	cases := map[string]string{
		"":                                    "https://api.tavily.com/usage",
		"https://api.tavily.com/search":       "https://api.tavily.com/usage",
		"https://proxy.example/tavily/search": "https://proxy.example/tavily/usage",
		"https://proxy.example/tavily/":       "https://proxy.example/tavily/usage",
	}
	for in, want := range cases {
		got, err := tavilyUsageURL(in)
		if err != nil || got != want {
			t.Fatalf("tavilyUsageURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}
