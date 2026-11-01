package auth

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

// freePort 申请一个空闲端口并立即释放，供固定回调端口用例使用。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("申请空闲端口失败: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// TestPKCEJSONTokenExchange 覆盖 Claude 订阅式 OAuth 的关键差异：
// 固定 localhost 端口、JSON 换 token（含 state 回传）、extra_credentials 落库。
func TestPKCEJSONTokenExchange(t *testing.T) {
	var gotBody map[string]string
	var gotContentType string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-claude-test","refresh_token":"rt-claude-test","expires_in":3600}`))
	}))
	defer idp.Close()

	c := New()
	port := freePort(t)
	cfg := &provider.OAuthConfig{
		AuthorizeURL: "https://claude.ai/oauth/authorize",
		TokenURL:     idp.URL,
		ClientID:     "client-abc",
		Scopes:       []string{"user:profile", "user:inference"},
		RedirectPath: "/callback",
		RedirectHost: "localhost",
		RedirectPort: port,
		TokenFormat:  "json",
		ExtraAuthParams: map[string]string{
			"code": "true",
		},
		ExtraCredentials: map[string]string{
			"oauth_client_id": "client-abc",
		},
	}

	s, err := c.StartPKCE("claude-sub", cfg)
	if err != nil {
		t.Fatalf("StartPKCE: %v", err)
	}

	wantRedirect := "http://localhost:" + strconv.Itoa(port) + "/callback"
	if s.RedirectURI != wantRedirect {
		t.Fatalf("redirect_uri = %q, want %q", s.RedirectURI, wantRedirect)
	}
	for _, frag := range []string{
		"code=true", "code_challenge_method=S256", "state=" + s.State,
		"redirect_uri=http%3A%2F%2Flocalhost%3A" + strconv.Itoa(port) + "%2Fcallback",
		"scope=user%3Aprofile+user%3Ainference",
	} {
		if !strings.Contains(s.AuthorizeURL, frag) {
			t.Fatalf("AuthorizeURL 缺少 %q: %s", frag, s.AuthorizeURL)
		}
	}

	// 状态不匹配必须拒绝，且不能消耗掉授权码。
	// 固定端口会复用同一地址，必须禁用 keep-alive，否则第二次请求可能
	// 落在上一个 server 的旧连接上（handler 捕获的是旧会话）。
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/callback?state=wrong&code=denied")
	if err != nil {
		t.Fatalf("回调请求失败: %v", err)
	}
	resp.Body.Close()
	if sess := c.Get(s.ID); sess.Status != StatusFailed {
		t.Fatalf("state 不匹配后状态 = %s, want failed", sess.Status)
	}

	// 用新会话走成功路径。
	s2, err := c.StartPKCE("claude-sub", cfg)
	if err != nil {
		t.Fatalf("StartPKCE(2): %v", err)
	}
	resp, err = client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/callback?state=" + s2.State + "&code=auth-code-1")
	if err != nil {
		t.Fatalf("回调请求失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if sess := c.Get(s2.ID); sess.Status != StatusSuccess {
		t.Fatalf("会话失败: status=%s err=%s body=%s", sess.Status, sess.Err, body)
	}

	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotContentType)
	}
	for k, want := range map[string]string{
		"grant_type": "authorization_code", "code": "auth-code-1",
		"client_id": "client-abc", "redirect_uri": wantRedirect, "state": s2.State,
	} {
		if gotBody[k] != want {
			t.Fatalf("token 请求字段 %s = %q, want %q", k, gotBody[k], want)
		}
	}
	if gotBody["code_verifier"] == "" {
		t.Fatal("token 请求缺少 code_verifier")
	}

	sess := c.Get(s2.ID)
	if sess.Status != StatusSuccess || sess.Credential == nil {
		t.Fatalf("会话状态 = %s err=%s, want success", sess.Status, sess.Err)
	}
	cred := sess.Credential.Credentials
	if cred["access_token"] != "at-claude-test" || cred["refresh_token"] != "rt-claude-test" {
		t.Fatalf("凭证 = %v", cred)
	}
	if cred["oauth_client_id"] != "client-abc" {
		t.Fatalf("extra_credentials 未落库: %v", cred)
	}
	if cred["expires_at"] == "" {
		t.Fatalf("缺少 expires_at: %v", cred)
	}
}

// TestPKCEStrictPortBusy 固定端口被占用时必须直接报错（预注册客户端不能换端口）。
func TestPKCEStrictPortBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	c := New()
	_, err = c.StartPKCE("claude-sub", &provider.OAuthConfig{
		AuthorizeURL: "https://claude.ai/oauth/authorize",
		TokenURL:     "https://platform.claude.com/v1/oauth/token",
		ClientID:     "client-abc",
		RedirectPort: port,
	})
	if err == nil || !strings.Contains(err.Error(), "被占用") {
		t.Fatalf("期望端口占用错误，实际: %v", err)
	}
}

// TestPKCEFormFallback 未声明 TokenFormat 的渠道保持 form 编码（回归保护）。
func TestPKCEFormFallback(t *testing.T) {
	var gotContentType, gotForm string
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		gotForm = string(raw)
		_, _ = w.Write([]byte(`{"access_token":"at-form","refresh_token":"rt-form"}`))
	}))
	defer idp.Close()

	c := New()
	port := freePort(t)
	cfg := &provider.OAuthConfig{
		AuthorizeURL: "https://example.com/authorize",
		TokenURL:     idp.URL,
		ClientID:     "form-client",
		RedirectPath: "/oauth/callback",
		RedirectPort: port,
	}
	s, err := c.StartPKCE("demo", cfg)
	if err != nil {
		t.Fatalf("StartPKCE: %v", err)
	}
	resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/oauth/callback?state=" + s.State + "&code=c1")
	if err != nil {
		t.Fatalf("回调请求失败: %v", err)
	}
	resp.Body.Close()

	if gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	if !strings.Contains(gotForm, "code=c1") || !strings.Contains(gotForm, "client_id=form-client") {
		t.Fatalf("form 请求体 = %q", gotForm)
	}
	if sess := c.Get(s.ID); sess.Status != StatusSuccess || sess.Credential.Credentials["access_token"] != "at-form" {
		t.Fatalf("会话 = %+v", sess)
	}
}

// TestPKCEIDTokenClaims 覆盖 Codex 式响应：token 里带 id_token，
// 账号标识藏在嵌套声明里，落库时还要补 base_url/model 默认值。
func TestPKCEIDTokenClaims(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{
		"email":"dev@example.com",
		"https://api.openai.com/auth":{"chatgpt_account_id":"acct-42"}
	}`))
	idToken := "eyJhbGciOiJub25lIn0." + payload + ".sig"

	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at-codex","refresh_token":"rt-codex","id_token":"` + idToken + `"}`))
	}))
	defer idp.Close()

	c := New()
	port := freePort(t)
	cfg := &provider.OAuthConfig{
		AuthorizeURL: "https://auth.openai.com/oauth/authorize",
		TokenURL:     idp.URL,
		ClientID:     "codex-client",
		RedirectPath: "/auth/callback",
		RedirectPort: port,
		IDTokenClaims: map[string]string{
			"account_id": "https://api.openai.com/auth#chatgpt_account_id",
		},
		ExtraCredentials: map[string]string{
			"base_url": "https://chatgpt.com/backend-api/codex",
			"model":    "gpt-5-codex",
		},
	}
	s, err := c.StartPKCE("codex", cfg)
	if err != nil {
		t.Fatalf("StartPKCE: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/auth/callback?state=" + s.State + "&code=c9")
	if err != nil {
		t.Fatalf("回调请求失败: %v", err)
	}
	resp.Body.Close()

	sess := c.Get(s.ID)
	if sess.Status != StatusSuccess || sess.Credential == nil {
		t.Fatalf("会话状态 = %s err=%s", sess.Status, sess.Err)
	}
	cred := sess.Credential.Credentials
	if cred["account_id"] != "acct-42" {
		t.Fatalf("id_token 声明未落库: %v", cred)
	}
	if cred["id_token"] != idToken {
		t.Fatalf("id_token 未保留")
	}
	if cred["base_url"] != "https://chatgpt.com/backend-api/codex" || cred["model"] != "gpt-5-codex" {
		t.Fatalf("默认凭证缺失: %v", cred)
	}
}

func TestSubmitPKCECallbackManual(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"manual-at","refresh_token":"manual-rt","expires_in":3600}`))
	}))
	defer idp.Close()

	c := New()
	cfg := &provider.OAuthConfig{
		AuthorizeURL: "https://example.com/authorize",
		TokenURL:     idp.URL,
		ClientID:     "client-manual",
		RedirectPath: "/callback",
		RedirectPort: freePort(t),
	}
	s, err := c.StartPKCE("manual-provider", cfg)
	if err != nil {
		t.Fatalf("StartPKCE: %v", err)
	}
	// state 不匹配必须拒绝，且会话保持 pending 供重试。
	if err := c.SubmitPKCECallback(s.ID, "http://localhost:1/callback?state=wrong&code=x"); err == nil {
		t.Fatal("state 不匹配应报错")
	}
	if got := c.Get(s.ID); got.Status != StatusPending {
		t.Fatalf("state 错误后状态 = %s, want pending", got.Status)
	}
	// 完整回调地址（含正确 state）。
	if err := c.SubmitPKCECallback(s.ID, s.RedirectURI+"?code=manual-code&state="+s.State); err != nil {
		t.Fatalf("手动回调失败: %v", err)
	}
	got := c.Get(s.ID)
	if got.Status != StatusSuccess || got.Credential == nil || got.Credential.Credentials["access_token"] != "manual-at" {
		t.Fatalf("会话 = %+v", got)
	}

	// 只粘贴裸 code（无 state）也应能完成。
	s2, err := c.StartPKCE("manual-provider", cfg)
	if err != nil {
		t.Fatalf("StartPKCE(2): %v", err)
	}
	if err := c.SubmitPKCECallback(s2.ID, "bare-code"); err != nil {
		t.Fatalf("裸 code 失败: %v", err)
	}
	if got := c.Get(s2.ID); got.Status != StatusSuccess {
		t.Fatalf("裸 code 后状态 = %s", got.Status)
	}
}
