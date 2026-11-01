package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aether-cockpit/internal/core/tools"
)

func mcpServerReq(t *testing.T, h http.Handler, body string, remote string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.RemoteAddr = remote
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMCPServerTools(t *testing.T) {
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"title":"Go","url":"https://go.dev","content":"language"}]}`))
	}))
	defer searchSrv.Close()
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><style>.x{}</style></head><body><h1>Hello</h1><p>World &amp; more</p><script>bad()</script></body></html>`))
	}))
	defer pageSrv.Close()
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("image path = %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "img-model" {
			t.Errorf("model = %v", body["model"])
		}
		_, _ = w.Write([]byte(`{"data":[{"url":"https://img.example/1.png"}]}`))
	}))
	defer imgSrv.Close()

	g, _, _ := agentApplyTestGateway(t)
	g.settingsMu.Lock()
	g.settings.Tools = &ToolsConfig{
		WebSearch: &tools.SearchConfig{Provider: "tavily", Endpoint: searchSrv.URL, APIKey: "k"},
		Image:     &tools.ImageConfig{BaseURL: imgSrv.URL, APIKey: "ik", Model: "img-model"},
	}
	g.settingsMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", g.handleMCPServer)

	// initialize
	rec := mcpServerReq(t, mux, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, "127.0.0.1:5555")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "2025-06-18") {
		t.Fatalf("initialize = %d %s", rec.Code, rec.Body.String())
	}
	// initialized notification
	rec = mcpServerReq(t, mux, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "127.0.0.1:5555")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("initialized = %d", rec.Code)
	}
	// tools/list
	rec = mcpServerReq(t, mux, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "127.0.0.1:5555")
	var list struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("tools/list: %s", rec.Body.String())
	}
	names := []string{}
	for _, tl := range list.Result.Tools {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "web_search,web_fetch,generate_image" {
		t.Fatalf("tools = %v", names)
	}

	// web_search
	rec = mcpServerReq(t, mux, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"web_search","arguments":{"query":"go","max_results":2}}}`, "127.0.0.1:5555")
	if !strings.Contains(rec.Body.String(), "https://go.dev") || strings.Contains(rec.Body.String(), `"isError":true`) {
		t.Fatalf("search = %s", rec.Body.String())
	}
	// web_fetch
	rec = mcpServerReq(t, mux, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"web_fetch","arguments":{"url":"`+pageSrv.URL+`"}}}`, "127.0.0.1:5555")
	body := rec.Body.String()
	if !strings.Contains(body, "Hello") || !strings.Contains(body, "World") || !strings.Contains(body, "more") || strings.Contains(body, "bad()") || strings.Contains(body, "<h1>") {
		t.Fatalf("fetch = %s", body)
	}
	// generate_image
	rec = mcpServerReq(t, mux, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"generate_image","arguments":{"prompt":"a cat"}}}`, "127.0.0.1:5555")
	if !strings.Contains(rec.Body.String(), "img.example") {
		t.Fatalf("image = %s", rec.Body.String())
	}
	// 非本机且无 Key：拒绝
	rec = mcpServerReq(t, mux, `{"jsonrpc":"2.0","id":6,"method":"tools/list"}`, "192.0.2.9:5555")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("remote = %d", rec.Code)
	}
}

func TestMCPServerRequiresConfiguredKey(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-", APIKey: "sk-test"})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", g.handleMCPServer)

	rec := mcpServerReq(t, mux, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, "127.0.0.1:5555")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no key = %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Authorization", "Bearer sk-test")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("with key = %d %s", rec.Code, rec.Body.String())
	}
}
