package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/clients"
)

func TestProfileCompatSaveUseDelete(t *testing.T) {
	dir := t.TempDir()
	// 隔离 HOME：否则 clients.New() 会读到本机真实 Agent 配置，save 会把它们
	// 也快照进来，use 甚至会写回真实文件。
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
	target := filepath.Join(dir, "probe.env")
	clientsPath := filepath.Join(dir, "clients.json")
	defs := `{"clients":[{"id":"probe","name":"Probe CLI","kind":"envfile","path":` + strconvQuote(target) + `,
		"keys":{"base_url":"OPENAI_BASE_URL","api_key":"OPENAI_API_KEY","model":"OPENAI_MODEL"}}]}`
	if err := os.WriteFile(clientsPath, []byte(defs), 0o600); err != nil {
		t.Fatal(err)
	}
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: "-", CustomClientsPath: clientsPath})

	mgr := g.clientsManager()
	if mgr == nil {
		t.Fatal("clients manager 不可用")
	}
	if _, err := mgr.Apply("probe", clients.ApplyParams{BaseURL: "http://127.0.0.1:9999", APIKey: "k", Model: "mock-native/echo-pro"}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/profile/{action}", g.handleProfileActionCompat)
	call := func(action, name string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/profile/"+action, strings.NewReader(`{"name":"`+name+`"}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := call("save", "工作")
	if rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	g.settingsMu.Lock()
	if len(g.settings.Profiles) != 1 {
		g.settingsMu.Unlock()
		t.Fatalf("profiles = %+v", g.settings.Profiles)
	}
	var probe *ProfileClient
	for i := range g.settings.Profiles[0].Clients {
		if g.settings.Profiles[0].Clients[i].ClientID == "probe" {
			probe = &g.settings.Profiles[0].Clients[i]
		}
	}
	g.settingsMu.Unlock()
	if probe == nil || probe.Model != "mock-native/echo-pro" || probe.Origin != "http://127.0.0.1:9999" {
		t.Fatalf("snapshot = %+v", probe)
	}

	rec = call("use", "工作")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"changed":1`) {
		t.Fatalf("use = %d %s", rec.Code, rec.Body.String())
	}
	var st struct {
		Profiles []struct {
			Name string `json:"name"`
		} `json:"profiles"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if len(st.Profiles) != 1 || st.Profiles[0].Name != "工作" {
		t.Fatalf("state profiles = %+v", st.Profiles)
	}

	rec = call("delete", "工作")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
	g.settingsMu.Lock()
	n := len(g.settings.Profiles)
	g.settingsMu.Unlock()
	if n != 0 {
		t.Fatalf("删除后仍有 %d 个方案", n)
	}
}

func TestAetherSyncReturnsState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USERPROFILE", dir)
	t.Setenv("HOME", dir)
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: "-"})
	rec := httptest.NewRecorder()
	g.handleAetherSync(rec, httptest.NewRequest(http.MethodPost, "/api/sync", strings.NewReader(`{}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var st map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if _, ok := st["agents"].([]any); !ok {
		t.Fatalf("state 缺 agents 数组: %v", st["agents"])
	}
	if _, ok := st["profiles"].([]any); !ok {
		t.Fatalf("state 缺 profiles 数组: %v", st["profiles"])
	}
}
