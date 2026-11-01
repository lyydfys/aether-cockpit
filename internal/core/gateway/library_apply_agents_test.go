package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/library"
)

// agentApplyTestGateway 建一个隔离 HOME + 临时库的 Gateway，并注册本组路由。
func agentApplyTestGateway(t *testing.T) (*Gateway, string, http.Handler) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.library = library.NewStore(filepath.Join(t.TempDir(), "library.json"))
	g.library.SetMCPApplyHook(g.rewriteMCPForRelay)

	mux := http.NewServeMux()
	for _, kind := range []string{"servers", "skills", "hooks", "subagents"} {
		mux.HandleFunc("POST /api/library/"+kind+"/agents", g.handleApplyAgents(kind))
		mux.HandleFunc("POST /api/library/"+kind+"/agents-some", g.handleApplyAgentsSome(kind))
		mux.HandleFunc("POST /api/library/"+kind+"/agents-all", g.handleApplyAgentsAll(kind))
		mux.HandleFunc("POST /api/library/"+kind+"/remove-all", g.handleRemoveAllKind(kind))
	}
	mux.HandleFunc("POST /api/library/subagents/save", g.handleSubagentSave)
	mux.HandleFunc("POST /api/library/mcp-signin", g.handleMCPSigninStart)
	mux.HandleFunc("GET /api/library/mcp-signin/{id}", g.handleMCPSigninPoll)
	mux.HandleFunc("POST /api/library/mcp-signin/{id}/cancel", g.handleMCPSigninCancel)
	mux.HandleFunc("POST /api/library/mcp-signout", g.handleMCPSignout)
	mux.HandleFunc("POST /mcp/{name}", g.handleMCPRelay)
	mux.HandleFunc("POST /api/library/servers/tools", g.handleMCPToolsSave)
	mux.HandleFunc("POST /api/library/servers/save", g.handleLibraryServerSave)
	mux.HandleFunc("POST /api/library/skills/group", g.handleSkillGroup)
	mux.HandleFunc("POST /api/library/skills/ungroup", g.handleSkillUngroup)
	mux.HandleFunc("POST /api/library/skills/remove-gone", g.handleSkillsRemoveGone)
	return g, home, mux
}

func applyReq(t *testing.T, h http.Handler, target, body string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", target, strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s = %d %s", target, rec.Code, rec.Body.String())
	}
	out := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func viewAgents(t *testing.T, out map[string]any, list, name string) []string {
	t.Helper()
	rows, _ := out[list].([]any)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["name"] != name {
			continue
		}
		ids, _ := row["agents"].([]any)
		got := make([]string, 0, len(ids))
		for _, id := range ids {
			got = append(got, id.(string))
		}
		return got
	}
	t.Fatalf("%s 里没有 %q: %v", list, name, rows)
	return nil
}

func sameAgents(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]bool{}
	for _, x := range a {
		m[x] = true
	}
	for _, x := range b {
		if !m[x] {
			return false
		}
	}
	return true
}

func TestLibraryApplyAgentsServers(t *testing.T) {
	g, home, h := agentApplyTestGateway(t)
	if _, err := g.library.SaveMCP(library.MCPEntry{
		Name: "demo", Config: json.RawMessage(`{"type":"http","url":"https://example.com/mcp"}`),
	}); err != nil {
		t.Fatal(err)
	}

	out := applyReq(t, h, "/api/library/servers/agents",
		`{"name":"demo","agents":["claude_code","codex_cli"]}`)
	claude, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if got := viewAgents(t, out, "servers", "demo"); !sameAgents(got, []string{"claude_code", "codex_cli"}) {
		t.Fatalf("servers agents = %v (result=%v claude=%s)", got, out["result"], claude)
	}
	changed, _ := out["result"].(map[string]any)["changed"].([]any)
	if len(changed) != 2 {
		t.Fatalf("changed = %v", changed)
	}
	claude, _ = os.ReadFile(filepath.Join(home, ".claude.json"))
	if !strings.Contains(string(claude), `"demo"`) {
		t.Fatalf(".claude.json 未写入: %s", claude)
	}
	codex, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(string(codex), "demo") {
		t.Fatalf("codex config 未写入: %s", codex)
	}

	out = applyReq(t, h, "/api/library/servers/agents", `{"name":"demo","agents":["claude_code"]}`)
	if got := viewAgents(t, out, "servers", "demo"); !sameAgents(got, []string{"claude_code"}) {
		t.Fatalf("removal agents = %v", got)
	}
	codex, _ = os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if strings.Contains(string(codex), "demo") {
		t.Fatalf("codex config 未移除: %s", codex)
	}

	out = applyReq(t, h, "/api/library/servers/agents-some",
		`{"names":["demo"],"agents":["gemini_cli"],"on":true}`)
	if got := viewAgents(t, out, "servers", "demo"); !sameAgents(got, []string{"claude_code", "gemini_cli"}) {
		t.Fatalf("agents-some on = %v", got)
	}
	out = applyReq(t, h, "/api/library/servers/agents-some",
		`{"names":["demo"],"agents":["gemini_cli"],"on":false}`)
	if got := viewAgents(t, out, "servers", "demo"); !sameAgents(got, []string{"claude_code"}) {
		t.Fatalf("agents-some off = %v", got)
	}

	out = applyReq(t, h, "/api/library/servers/remove-all", `{"names":["demo"]}`)
	if rows, _ := out["servers"].([]any); len(rows) != 0 {
		t.Fatalf("remove-all 后仍有服务器: %v", rows)
	}
}

func TestLibraryApplyAgentsSkillsShared(t *testing.T) {
	g, home, h := agentApplyTestGateway(t)
	if _, err := g.library.SaveSkill(library.SkillEntry{
		Name: "sk", Content: "---\nname: sk\ndescription: demo\n---\nbody",
	}); err != nil {
		t.Fatal(err)
	}

	out := applyReq(t, h, "/api/library/skills/agents",
		`{"name":"sk","agents":["codex_cli","gemini_cli"]}`)
	got := viewAgents(t, out, "skills", "sk")
	if !sameAgents(got, []string{"codex_cli", "gemini_cli", "opencode"}) {
		t.Fatalf("shared dir 应按目录探测出三个 Agent: %v", got)
	}
	shared := filepath.Join(home, ".agents", "skills", "sk", "SKILL.md")
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("共享技能未写入: %v", err)
	}

	// 只关掉一个共享 Agent：目录仍被其余 Agent 使用，必须保留。
	out = applyReq(t, h, "/api/library/skills/agents", `{"name":"sk","agents":["gemini_cli"]}`)
	if _, err := os.Stat(shared); err != nil {
		t.Fatalf("还有 Agent 在使用时删掉了共享目录")
	}
	if got := viewAgents(t, out, "skills", "sk"); !sameAgents(got, []string{"codex_cli", "gemini_cli", "opencode"}) {
		t.Fatalf("shared agents = %v", got)
	}

	// 全部关掉后目录移除。
	out = applyReq(t, h, "/api/library/skills/agents-all", `{"agents":["codex_cli","gemini_cli","opencode","claude_code"],"on":false}`)
	if _, err := os.Stat(shared); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(filepath.Join(home, ".agents", "skills", "sk"))
		t.Fatalf("共享技能目录未移除 entries=%v result=%v", entries, out["result"])
	}
	if got := viewAgents(t, out, "skills", "sk"); len(got) != 0 {
		t.Fatalf("skills agents = %v", got)
	}
}

func TestLibraryApplyAgentsHooksAndSubagents(t *testing.T) {
	g, home, h := agentApplyTestGateway(t)
	if _, err := g.library.SaveHook(library.HookEntry{
		Name: "hello-hook", Event: "turn-end", Command: "echo hello",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.library.SaveAgent(library.AgentEntry{
		Name: "helper", Content: "---\nname: helper\ndescription: demo\n---\nbody",
	}); err != nil {
		t.Fatal(err)
	}

	out := applyReq(t, h, "/api/library/hooks/agents", `{"name":"hello-hook","agents":["claude_code"]}`)
	if got := viewAgents(t, out, "hooks", "hello-hook"); !sameAgents(got, []string{"claude_code"}) {
		t.Fatalf("hooks agents = %v", got)
	}
	settings, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(string(settings), "echo hello") {
		t.Fatalf("hook 未写入: %s", settings)
	}
	out = applyReq(t, h, "/api/library/hooks/agents", `{"name":"hello-hook","agents":[]}`)
	if got := viewAgents(t, out, "hooks", "hello-hook"); len(got) != 0 {
		t.Fatalf("hook 未撤销: %v", got)
	}

	out = applyReq(t, h, "/api/library/subagents/agents", `{"name":"helper","agents":["claude_code"]}`)
	if got := viewAgents(t, out, "subagents", "helper"); !sameAgents(got, []string{"claude_code"}) {
		t.Fatalf("subagents agents = %v", got)
	}
	md := filepath.Join(home, ".claude", "agents", "helper.md")
	if _, err := os.Stat(md); err != nil {
		t.Fatalf("子代理文件未写入: %v", err)
	}
	out = applyReq(t, h, "/api/library/subagents/agents", `{"name":"helper","agents":[]}`)
	if got := viewAgents(t, out, "subagents", "helper"); len(got) != 0 {
		t.Fatalf("subagent 未撤销: %v", got)
	}
	if _, err := os.Stat(md); !os.IsNotExist(err) {
		t.Fatalf("子代理文件未删除")
	}
}
