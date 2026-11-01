package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func agentReq(t *testing.T, g *Gateway, method, target, body string) map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/preview/{id}", g.handleAgentPreview)
	mux.HandleFunc("POST /api/agents/{action}", g.handleAgentAction)
	mux.HandleFunc("POST /api/agents/{action}/{id}", g.handleAgentAction)
	mux.HandleFunc("GET /api/agent-models/{id}", g.handleAgentModels)
	mux.HandleFunc("POST /api/agent-models/{id}", g.handleAgentModels)
	mux.HandleFunc("POST /api/agent-fast", g.handleAgentFast)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s = %d %s", method, target, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAgentModelsOrderAndOnly(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	got := agentReq(t, g, "GET", "/api/agent-models/claude_code", "")
	models, _ := got["models"].([]any)
	if len(models) < 3 {
		t.Fatalf("models = %d", len(models))
	}
	first := models[0].(map[string]any)["id"].(string)
	second := models[1].(map[string]any)["id"].(string)

	out := agentReq(t, g, "POST", "/api/agent-models/claude_code", `{"order":["`+second+`","`+first+`"],"only":true,"hidden":["`+first+`"]}`)
	if out["ordered"] != true || out["only"] != true {
		t.Fatalf("prefs = %#v", out)
	}
	list := out["models"].([]any)
	if list[0].(map[string]any)["id"] != second {
		t.Fatalf("order not applied: %#v", list[0])
	}
	for _, x := range list {
		if m := x.(map[string]any); m["id"] == first && m["hidden"] != true {
			t.Fatalf("hidden not applied: %#v", m)
		}
	}
	// 重读仍保留
	again := agentReq(t, g, "GET", "/api/agent-models/claude_code", "")
	if again["only"] != true || again["ordered"] != true {
		t.Fatalf("reload prefs = %#v", again)
	}
}

func TestAgentArrangeStagePreview(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	arr := agentReq(t, g, "POST", "/api/agents/arrange", `{"order":["claude_code","codex"],"hidden":["gemini"],"shown":[]}`)
	if arr["agentOrder"].([]any)[0] != "claude_code" {
		t.Fatalf("arrange = %#v", arr)
	}
	stage := agentReq(t, g, "POST", "/api/agents/stage/claude_code", `{"field":"model","value":"mock-native/echo-pro"}`)
	if len(stage["agents"].([]any)) == 0 {
		t.Fatalf("stage state = %#v", stage)
	}
	prev := agentReq(t, g, "GET", "/api/agents/preview/claude_code", "")
	if _, ok := prev["changes"]; !ok {
		t.Fatalf("preview = %#v", prev)
	}
	fast := agentReq(t, g, "POST", "/api/agent-fast", `{"for":"claude_code","ref":"mock-native/echo-pro","fast":true}`)
	if fast["ok"] != true {
		t.Fatalf("fast = %#v", fast)
	}
	rescan := agentReq(t, g, "POST", "/api/agents/rescan", `{}`)
	if _, ok := rescan["agents"]; !ok {
		t.Fatalf("rescan = %#v", rescan)
	}
}

func TestAgentStateHasModelRefs(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	st := g.buildAetherState()
	if len(st.Agents) == 0 {
		t.Fatal("应至少有本机客户端")
	}
	a := st.Agents[0]
	if a.Models == nil || a.Models["listed"].(int) == 0 {
		t.Fatalf("models summary = %#v", a.Models)
	}
	refs := 0
	for _, o := range a.Fields[0].Options {
		if o.Ref != "" {
			refs++
		}
	}
	if refs == 0 {
		t.Fatal("模型选项应带 ref（Agents 页据此显示接入开关）")
	}
}

func TestAgentConnectDisconnectCustomClient(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "probe.env")
	clientsPath := filepath.Join(dir, "clients.json")
	defs := `{"clients":[{"id":"probe","name":"Probe CLI","kind":"envfile","path":` + strconvQuote(target) + `,
		"keys":{"base_url":"OPENAI_BASE_URL","api_key":"OPENAI_API_KEY","model":"OPENAI_MODEL"}}]}`
	if err := os.WriteFile(clientsPath, []byte(defs), 0o600); err != nil {
		t.Fatal(err)
	}
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: "-", CustomClientsPath: clientsPath})

	// 预置用户原有配置，断开后必须原样恢复。
	original := "ORIGINAL=1\n"
	if err := os.WriteFile(target, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/agents/{action}/{id}", g.handleAgentAction)
	mux.HandleFunc("GET /api/agents/preview/{id}", g.handleAgentPreview)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := call("POST", "/api/agents/connect/probe", "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("connect = %d %s", rec.Code, rec.Body.String())
	}
	raw, _ := os.ReadFile(target)
	if !strings.Contains(string(raw), "OPENAI_BASE_URL") {
		t.Fatalf("connect 未写配置: %s", raw)
	}

	rec = call("GET", "/api/agents/preview/probe", "")
	var pv map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &pv)
	if pv["error"] != nil {
		t.Fatalf("preview error: %v", pv["error"])
	}
	if len(pv["changes"].([]any)) == 0 || len(pv["changes"].([]any)[0].(map[string]any)["lines"].([]any)) == 0 {
		t.Fatalf("preview changes = %#v", pv)
	}

	rec = call("POST", "/api/agents/disconnect/probe", "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("disconnect = %d %s", rec.Code, rec.Body.String())
	}
	raw, _ = os.ReadFile(target)
	if string(raw) != original {
		t.Fatalf("disconnect 未还原: %q", raw)
	}

	// 首次接入（原文件不存在）再断开：文件应被删除。
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if rec := call("POST", "/api/agents/connect/probe", "{}"); rec.Code != http.StatusOK {
		t.Fatalf("reconnect = %d", rec.Code)
	}
	if rec := call("POST", "/api/agents/disconnect/probe", "{}"); rec.Code != http.StatusOK {
		t.Fatalf("disconnect2 = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("断开首次接入应删除文件: %v", err)
	}
}
