package gateway

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/library"
)

func TestServerSaveKeepsPreRegisteredOAuth(t *testing.T) {
	g, _, h := agentApplyTestGateway(t)
	out := applyReq(t, h, "/api/library/servers/save",
		`{"old":"","server":{"name":"demo","transport":"http","url":"https://mcp.example/mcp","agents":[],"oauth":{"clientId":"cid-1","clientSecret":"s3cr3t-value","callbackPort":43123}}}`)
	row := findRow(t, out, "servers", "demo")
	o, _ := row["oauth"].(map[string]any)
	if o["clientId"] != "cid-1" || o["callbackPort"] != float64(43123) {
		t.Fatalf("oauth = %v", row["oauth"])
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "s3cr3t-value") || strings.Contains(string(raw), "clientSecret") {
		t.Fatalf("clientSecret 不应出现在视图: %s", raw)
	}
	for _, m := range g.library.List().MCPServers {
		if m.Name == "demo" {
			if m.OAuth == nil || m.OAuth.ClientSecret != "s3cr3t-value" {
				t.Fatalf("secret 未保存: %+v", m.OAuth)
			}
		}
	}

	// 原始条目（/api/library 的 mcp_servers）也不能带 secret。
	rec := httptest.NewRecorder()
	g.handleLibrary(rec, httptest.NewRequest("GET", "/api/library", nil))
	if strings.Contains(rec.Body.String(), "s3cr3t-value") || strings.Contains(rec.Body.String(), `"clientSecret"`) {
		t.Fatalf("/api/library 泄露 clientSecret: %s", rec.Body.String())
	}

	// 再次保存只带 clientId（留空 secret）→ 沿用旧 secret。
	applyReq(t, h, "/api/library/servers/save",
		`{"old":"demo","server":{"name":"demo","transport":"http","url":"https://mcp.example/mcp","agents":[],"oauth":{"clientId":"cid-1","callbackPort":43123}}}`)
	for _, m := range g.library.List().MCPServers {
		if m.Name == "demo" {
			if m.OAuth == nil || m.OAuth.ClientSecret != "s3cr3t-value" || m.OAuth.CallbackPort != 43123 {
				t.Fatalf("留空时应沿用旧 secret/port: %+v", m.OAuth)
			}
		}
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// 预注册客户端：授权服务器不支持动态注册（无 registration_endpoint）也能登录。
func TestMCPSigninPreRegisteredClient(t *testing.T) {
	var asURL, mcpURL string
	var gotClient, gotSecret string
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prm":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource": mcpURL, "authorization_servers": []string{asURL},
			})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": asURL, "authorization_endpoint": asURL + "/authorize",
				"token_endpoint":                   asURL + "/token",
				"code_challenge_methods_supported": []string{"S256"},
				// 故意没有 registration_endpoint
			})
		case "/token":
			_ = r.ParseForm()
			gotClient = r.Form.Get("client_id")
			gotSecret = r.Form.Get("client_secret")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at-1", "refresh_token": "rt-1", "token_type": "Bearer", "expires_in": 3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer as.Close()
	asURL = as.URL

	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+asURL+`/prm"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcp.Close()
	mcpURL = mcp.URL + "/mcp"

	g, _, h := agentApplyTestGateway(t)
	port := freeTCPPort(t)
	if _, err := g.library.SaveMCP(library.MCPEntry{
		Name:   "demo",
		Config: json.RawMessage(`{"type":"http","url":"` + mcpURL + `"}`),
		OAuth:  &library.MCPOAuthClient{ClientID: "cid-pre", ClientSecret: "sec-pre", CallbackPort: port},
	}); err != nil {
		t.Fatal(err)
	}

	start := applyReq(t, h, "/api/library/mcp-signin", `{"name":"demo"}`)
	authURL, _ := start["url"].(string)
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	if q := u.Query(); q.Get("client_id") != "cid-pre" {
		t.Fatalf("client_id = %q", q.Get("client_id"))
	}
	if got := u.Query().Get("redirect_uri"); !strings.HasPrefix(got, "http://localhost:"+strconv.Itoa(port)+"/") {
		t.Fatalf("redirect_uri = %q，应使用 localhost 固定端口 %d", got, port)
	}
	// 完成回调（假 AS 的 token 端点校验 client/secret）。
	cb := u.Query().Get("redirect_uri") + "?code=code-1&state=" + url.QueryEscape(u.Query().Get("state"))
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

	var poll map[string]any
	id, _ := start["id"].(string)
	for i := 0; i < 100; i++ {
		poll = getReq(t, h, "/api/library/mcp-signin/"+id)
		if poll["state"] != "waiting" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if poll["state"] != "done" {
		t.Fatalf("poll = %v", poll)
	}
	if gotClient != "cid-pre" || gotSecret != "sec-pre" {
		t.Fatalf("token request client=%q secret=%q", gotClient, gotSecret)
	}
	rec := g.mcpAuthStore().get("demo")
	if rec == nil || rec.Client.ClientID != "cid-pre" || rec.Client.ClientSecret != "sec-pre" {
		t.Fatalf("pre-registered client 未保存: %+v", rec)
	}
}
