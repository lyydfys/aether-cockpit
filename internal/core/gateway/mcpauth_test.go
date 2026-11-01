package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseBearerParams(t *testing.T) {
	got := parseBearerParams(`Bearer resource_metadata="https://h/.well-known/oauth-protected-resource/mcp", scope="tools read"`)
	if got["resource_metadata"] != "https://h/.well-known/oauth-protected-resource/mcp" {
		t.Fatalf("resource_metadata = %q", got["resource_metadata"])
	}
	if got["scope"] != "tools read" {
		t.Fatalf("scope = %q", got["scope"])
	}
	if got := parseBearerParams(`Bearer error="invalid_token", error_description="expired"`); got["error_description"] != "expired" {
		t.Fatalf("error_description = %q", got["error_description"])
	}
}

func TestOAuthFlowAgainstFakeAS(t *testing.T) {
	var asURL string
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                asURL,
				"authorization_endpoint":                asURL + "/authorize",
				"token_endpoint":                        asURL + "/token",
				"registration_endpoint":                 asURL + "/register",
				"code_challenge_methods_supported":      []string{"S256"},
				"token_endpoint_auth_methods_supported": []string{"none"},
			})
		case "/register":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			uris, _ := body["redirect_uris"].([]any)
			if len(uris) != 1 || !strings.HasPrefix(uris[0].(string), "http://127.0.0.1:") {
				t.Errorf("redirect_uris = %v", uris)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "client-1"})
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code_verifier") != "verifier-1" || r.Form.Get("code") != "code-1" {
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

	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+as.URL+`/prm", scope="tools"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcp.Close()

	ctx := context.Background()
	hc := oauthHTTPClient()

	metaURL, scope, err := probeMCPAuth(ctx, mcp.URL+"/mcp", hc)
	if err != nil || metaURL != as.URL+"/prm" || scope != "tools" {
		t.Fatalf("probe = %q %q %v", metaURL, scope, err)
	}

	// 资源元数据由测试内联返回。
	prm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource": mcp.URL + "/mcp", "authorization_servers": []string{as.URL},
		})
	}))
	defer prm.Close()
	resMeta, err := discoverResource(ctx, mcp.URL+"/mcp", prm.URL, hc)
	if err != nil || len(resMeta.AuthorizationServers) != 1 {
		t.Fatalf("resource meta = %+v %v", resMeta, err)
	}
	asMeta, err := discoverAS(ctx, resMeta.AuthorizationServers[0], hc)
	if err != nil || asMeta.TokenEndpoint != as.URL+"/token" {
		t.Fatalf("as meta = %+v %v", asMeta, err)
	}
	if asMeta.CodeChallengeMethods[0] != "S256" {
		t.Fatalf("pkce methods = %v", asMeta.CodeChallengeMethods)
	}

	client, err := registerOAuthClient(ctx, asMeta, "http://127.0.0.1:4567/callback", "tools", hc)
	if err != nil || client.ClientID != "client-1" {
		t.Fatalf("register = %+v %v", client, err)
	}
	verifier, challenge, err := newPKCE()
	if err != nil || verifier == "" || challenge == "" || strings.ContainsAny(verifier, "+/=") {
		t.Fatalf("pkce = %q %q %v", verifier, challenge, err)
	}
	authURL := oauthAuthorizeURL(asMeta, client.ClientID, "http://127.0.0.1:4567/callback", "st-1", challenge, "tools", mcp.URL+"/mcp")
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("state") != "st-1" || q.Get("resource") != mcp.URL+"/mcp" {
		t.Fatalf("authorize query = %v", q)
	}

	// 令牌端点要求 verifier-1/code-1，这里直接用品。真实流程里 verifier 由 pending 保存。
	tok, err := exchangeOAuthCode(ctx, asMeta, client, "code-1", "verifier-1", "http://127.0.0.1:4567/callback", mcp.URL+"/mcp", hc)
	if err != nil || tok.AccessToken != "at-1" || tok.RefreshToken != "rt-1" {
		t.Fatalf("exchange = %+v %v", tok, err)
	}
	if tok.ExpiresAt <= time.Now().Unix() {
		t.Fatalf("expires_at 未按 expires_in 换算: %d", tok.ExpiresAt)
	}
}

func TestOAuthRefreshNeedsToken(t *testing.T) {
	var gotRefresh string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotRefresh = r.Form.Get("refresh_token")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-2", "expires_in": 60})
	}))
	defer srv.Close()
	meta := oauthASMeta{TokenEndpoint: srv.URL}
	client := oauthClient{ClientID: "c"}
	tok, err := refreshOAuthToken(context.Background(), meta, client, oauthToken{RefreshToken: "rt-9"}, "res", oauthHTTPClient())
	if err != nil || tok.AccessToken != "at-2" || tok.RefreshToken != "rt-9" || gotRefresh != "rt-9" {
		t.Fatalf("refresh = %+v %v (sent %q)", tok, err, gotRefresh)
	}
	if _, err := refreshOAuthToken(context.Background(), meta, client, oauthToken{}, "res", oauthHTTPClient()); err == nil {
		t.Fatal("没有 refresh_token 应报错")
	}
}
