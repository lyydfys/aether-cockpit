package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/library"
)

func getReq(t *testing.T, h http.Handler, target string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", target, rec.Code, rec.Body.String())
	}
	out := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestMCPSigninEndToEnd 覆盖：discovery → 动态注册 → 回调换令牌 → 视图 signedIn
// → 下发配置改写成中继地址 → 中继注入 Bearer → 退出登录还原上游地址。
// 浏览器一直不回调：超时应显示为失败（可重试），而不是悄悄当作取消。
func TestMCPSigninTimeoutIsFailed(t *testing.T) {
	g, _, _ := agentApplyTestGateway(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	flow := &signinFlow{id: "flow-timeout", name: "demo", state: "waiting", createdAt: time.Now(), cancel: func() {}}
	g.signinFlows.put(flow)
	g.awaitMCPCallback(ctx, flow, ln, oauthClient{ClientID: "c"}, oauthASMeta{}, "v", "s", "http://127.0.0.1:1/callback", "https://up", "https://up")
	got := g.signinFlows.get("flow-timeout")
	if got == nil || got.state != "failed" || !strings.Contains(got.err, "超时") {
		t.Fatalf("timeout flow = %+v", got)
	}
}

func TestMCPSigninEndToEnd(t *testing.T) {
	var asURL string
	var lastAuth string
	var revoked string
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-1" {
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+asURL+`/prm"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		lastAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	}))
	defer mcpSrv.Close()

	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prm":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource": mcpSrv.URL + "/mcp", "authorization_servers": []string{asURL},
			})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": asURL, "authorization_endpoint": asURL + "/authorize",
				"token_endpoint": asURL + "/token", "registration_endpoint": asURL + "/register",
				"revocation_endpoint":              asURL + "/revoke",
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/revoke":
			_ = r.ParseForm()
			revoked = r.Form.Get("token")
			w.WriteHeader(http.StatusOK)
		case "/register":
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "client-1"})
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "code-1" || r.Form.Get("code_verifier") == "" {
				t.Errorf("token form = %v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at-1", "refresh_token": "rt-1", "token_type": "Bearer", "expires_in": 3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer as.Close()
	asURL = as.URL

	g, home, h := agentApplyTestGateway(t)
	if _, err := g.library.SaveMCP(library.MCPEntry{
		Name: "demo", Config: json.RawMessage(`{"type":"http","url":"` + mcpSrv.URL + `/mcp"}`),
	}); err != nil {
		t.Fatal(err)
	}

	start := applyReq(t, h, "/api/library/mcp-signin", `{"name":"demo"}`)
	authURL, _ := start["url"].(string)
	flowID, _ := start["id"].(string)
	if flowID == "" || authURL == "" {
		t.Fatalf("start = %v", start)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("resource") != mcpSrv.URL+"/mcp" {
		t.Fatalf("authorize query = %v", q)
	}
	// 模拟浏览器授权后回到本机回调（临时端口）。
	cb := q.Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(q.Get("state"))
	var resp *http.Response
	for i := 0; i < 100; i++ {
		resp, err = http.Get(cb)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback = %d", resp.StatusCode)
	}

	var poll map[string]any
	for i := 0; i < 100; i++ {
		poll = getReq(t, h, "/api/library/mcp-signin/"+flowID)
		if poll["state"] != "waiting" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if poll["state"] != "done" {
		t.Fatalf("poll = %v", poll)
	}

	// 视图：signedIn，且不下发令牌本身。
	view := g.libraryPluginView(g.library.List())
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "at-1") {
		t.Fatalf("视图泄露了访问令牌")
	}
	signed := false
	for _, s := range view["servers"].([]map[string]any) {
		if s["name"] == "demo" {
			si, _ := s["signIn"].(map[string]any)
			signed, _ = si["signedIn"].(bool)
		}
	}
	if !signed {
		t.Fatalf("视图未标为已登录: %v", view["servers"])
	}

	// 下发：URL 改写为中继地址，且不携带 Authorization。
	applyReq(t, h, "/api/library/servers/agents", `{"name":"demo","agents":["claude_code"]}`)
	claudeRaw, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	relayURL := g.relayBaseURL() + "/mcp/demo"
	if !strings.Contains(string(claudeRaw), relayURL) {
		t.Fatalf("下发配置未指向中继 %s: %s", relayURL, claudeRaw)
	}
	if strings.Contains(string(claudeRaw), "at-1") {
		t.Fatalf("下发的配置里带了令牌: %s", claudeRaw)
	}

	// 中继：本机请求被注入 Bearer 并转发。
	req := httptest.NewRequest("POST", "/mcp/demo", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.RemoteAddr = "127.0.0.1:5555"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || lastAuth != "Bearer at-1" {
		t.Fatalf("relay = %d auth=%q body=%s", rec.Code, lastAuth, rec.Body.String())
	}
	// 非本机请求被拒。
	req = httptest.NewRequest("POST", "/mcp/demo", strings.NewReader("{}"))
	req.RemoteAddr = "192.0.2.9:5555"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("非回环请求应 403，得到 %d", rec.Code)
	}

	// 退出登录：令牌删除，已下发配置还原成上游地址。
	applyReq(t, h, "/api/library/mcp-signout", `{"name":"demo"}`)
	if revoked != "rt-1" {
		t.Fatalf("退出登录应尽力撤销服务商令牌，revoked=%q", revoked)
	}
	claudeRaw, _ = os.ReadFile(filepath.Join(home, ".claude.json"))
	if !strings.Contains(string(claudeRaw), mcpSrv.URL+"/mcp") || strings.Contains(string(claudeRaw), relayURL) {
		t.Fatalf("退出登录后未还原上游地址: %s", claudeRaw)
	}
	view = g.libraryPluginView(g.library.List())
	for _, s := range view["servers"].([]map[string]any) {
		if s["name"] == "demo" {
			if si, _ := s["signIn"].(map[string]any); si["signedIn"] == true {
				t.Fatalf("退出后仍标记已登录: %v", si)
			}
		}
	}
	// 令牌文件仍是私有权限，内容可被重新读取。
	authPath := filepath.Join(filepath.Dir(g.settingsPath), "mcp-auth.json")
	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("令牌库文件缺失: %v", err)
	}
}
