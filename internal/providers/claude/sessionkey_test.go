package claude

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// overrideClaudeURLs 把三个包级端点 var 覆盖到测试服务器，测试结束后恢复。
// 这些测试改包级状态，禁止 t.Parallel()。
func overrideClaudeURLs(t *testing.T, srvURL string) {
	t.Helper()
	oldOrgs, oldAuth, oldTok := claudeOrgsURL, claudeAuthorizeURL, claudeOAuthTokenURL
	claudeOrgsURL = srvURL + "/api/organizations"
	claudeAuthorizeURL = srvURL
	claudeOAuthTokenURL = srvURL + "/v1/oauth/token"
	t.Cleanup(func() {
		claudeOrgsURL, claudeAuthorizeURL, claudeOAuthTokenURL = oldOrgs, oldAuth, oldTok
	})
}

// sessionKeyFixtureServer 构造三步流全成功的一次性上游：
// 第 1 步返回 personal + team 两个组织（验证 team 优先），
// 第 2 步回显 redirect_uri（code + state），第 3 步发放 token。
// 三个端点的请求快照分别记录在返回的指针里供断言。
type sessionKeyFixture struct {
	orgMethod   string
	orgCookie   string
	authPath    string
	authOrigin  string
	authReferer string
	authCookie  string
	authBody    map[string]any
	tokenBody   map[string]any
	tokenCookie string

	orgsMu      sync.Mutex
	orgsCounter int32
}

func (f *sessionKeyFixture) countOrg() {
	f.orgsMu.Lock()
	defer f.orgsMu.Unlock()
	f.orgsCounter++
}

func (f *sessionKeyFixture) orgsSeen() int32 {
	f.orgsMu.Lock()
	defer f.orgsMu.Unlock()
	return f.orgsCounter
}

func newSessionKeyFixtureServer(t *testing.T) (*httptest.Server, *sessionKeyFixture) {
	t.Helper()
	fx := &sessionKeyFixture{}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/organizations", func(w http.ResponseWriter, r *http.Request) {
		fx.orgMethod = r.Method
		fx.countOrg()
		fx.orgCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[
			{"uuid":"org-personal","name":"Personal","raven_type":null},
			{"uuid":"org-team","name":"Team","raven_type":"team"},
			{"uuid":"org-late","name":"Late","raven_type":"team"}
		]`)
	})

	mux.HandleFunc("POST /v1/oauth/", func(w http.ResponseWriter, r *http.Request) {
		fx.authPath = r.URL.Path
		fx.authOrigin = r.Header.Get("Origin")
		fx.authReferer = r.Header.Get("Referer")
		fx.authCookie = r.Header.Get("Cookie")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &fx.authBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"redirect_uri":"`+claudeRedirectURI+`?code=acme-code&state=`+fx.authBody["state"].(string)+`"}`)
	})

	mux.HandleFunc("POST /v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &fx.tokenBody)
		fx.tokenCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"sk-ant-oat01-new","refresh_token":"sk-ant-ort01-new","expires_in":3600,"scope":"`+sessionKeyScope+`"}`)
	})

	// 换取成功后的正常对话端点（懒处理测试用）。
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg-sk","type":"message","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, fx
}

// TestExchangeSessionKeyThreeStepFlow 三步流全成功的契约测试：
// 方法 / URL / 头（Cookie、Origin、Referer）/ body 参数（PKCE、scope、redirect_uri）/
// 最终 draft 的凭证与过期时间。
func TestExchangeSessionKeyThreeStepFlow(t *testing.T) {
	srv, fx := newSessionKeyFixtureServer(t)
	overrideClaudeURLs(t, srv.URL)

	draft, err := exchangeSessionKey(context.Background(), srv.Client(), "sk-ant-sid01-test")
	if err != nil {
		t.Fatal(err)
	}

	// 第 1 步：GET + sessionKey Cookie。
	if fx.orgMethod != http.MethodGet {
		t.Fatalf("orgs method = %q", fx.orgMethod)
	}
	if fx.orgCookie != "sessionKey=sk-ant-sid01-test" {
		t.Fatalf("orgs cookie = %q", fx.orgCookie)
	}
	// 多组织优先 team：第 2 步打到 org-team 而不是第一个 org-personal。
	if fx.authPath != "/v1/oauth/org-team/authorize" {
		t.Fatalf("authorize path = %q", fx.authPath)
	}
	// 第 2 步：伪装网页端的 Origin/Referer + sessionKey Cookie。
	if fx.authOrigin != "https://claude.ai" || fx.authReferer != "https://claude.ai/new" {
		t.Fatalf("origin=%q referer=%q", fx.authOrigin, fx.authReferer)
	}
	if fx.authCookie != "sessionKey=sk-ant-sid01-test" {
		t.Fatalf("authorize cookie = %q", fx.authCookie)
	}

	// 第 2 步 body 参数契约。
	if fx.authBody["response_type"] != "code" {
		t.Fatalf("response_type = %v", fx.authBody["response_type"])
	}
	if fx.authBody["client_id"] != defaultClientID {
		t.Fatalf("client_id = %v", fx.authBody["client_id"])
	}
	if fx.authBody["organization_uuid"] != "org-team" {
		t.Fatalf("organization_uuid = %v", fx.authBody["organization_uuid"])
	}
	if fx.authBody["redirect_uri"] != claudeRedirectURI {
		t.Fatalf("redirect_uri = %v", fx.authBody["redirect_uri"])
	}
	if fx.authBody["scope"] != sessionKeyScope {
		t.Fatalf("scope = %v", fx.authBody["scope"])
	}
	if fx.authBody["code_challenge_method"] != "S256" {
		t.Fatalf("code_challenge_method = %v", fx.authBody["code_challenge_method"])
	}
	challenge, _ := fx.authBody["code_challenge"].(string)
	state, _ := fx.authBody["state"].(string)
	if len(challenge) != 43 || len(state) < 16 {
		t.Fatalf("challenge=%q state=%q", challenge, state)
	}

	// 第 3 步 body：code 剥掉 #state、code_verifier 与 challenge 满足 S256 关系。
	if fx.tokenBody["grant_type"] != "authorization_code" {
		t.Fatalf("grant_type = %v", fx.tokenBody["grant_type"])
	}
	if fx.tokenBody["code"] != "acme-code" {
		t.Fatalf("code = %v（应剥掉 #state）", fx.tokenBody["code"])
	}
	if fx.tokenBody["state"] != state {
		t.Fatalf("state = %v, want %v", fx.tokenBody["state"], state)
	}
	if fx.tokenBody["client_id"] != defaultClientID {
		t.Fatalf("client_id = %v", fx.tokenBody["client_id"])
	}
	if fx.tokenBody["redirect_uri"] != claudeRedirectURI {
		t.Fatalf("redirect_uri = %v", fx.tokenBody["redirect_uri"])
	}
	verifier, _ := fx.tokenBody["code_verifier"].(string)
	if len(verifier) != 43 {
		t.Fatalf("code_verifier = %q（应为 43 字符）", verifier)
	}
	sum := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); challenge != want {
		t.Fatalf("S256(challenge=%q) != sha256(verifier)=%q", challenge, want)
	}
	// 第 3 步不带 sessionKey Cookie（换 token 阶段与浏览器会话无关）。
	if fx.tokenCookie != "" {
		t.Fatalf("token 请求不应携带 sessionKey Cookie，got %q", fx.tokenCookie)
	}

	// draft 契约：access/refresh/expires_at。
	if draft.Credentials["access_token"] != "sk-ant-oat01-new" ||
		draft.Credentials["refresh_token"] != "sk-ant-ort01-new" {
		t.Fatalf("draft credentials = %+v", draft.Credentials)
	}
	exp := parseExpiry(draft.Credentials["expires_at"])
	if exp.IsZero() || !exp.After(time.Now().Add(30*time.Minute)) {
		t.Fatalf("expires_at = %v", draft.Credentials["expires_at"])
	}
	if draft.ExpiresAt != exp.Unix() {
		t.Fatalf("draft.ExpiresAt = %d, want %d", draft.ExpiresAt, exp.Unix())
	}
}

// TestExchangeSessionKeySingleOrgFallback 单组织时直接选用（无 team 也可用）。
func TestExchangeSessionKeySingleOrgFallback(t *testing.T) {
	var authPath string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/organizations", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"uuid":"org-solo","name":"Solo","raven_type":null}]`)
	})
	mux.HandleFunc("POST /v1/oauth/", func(w http.ResponseWriter, r *http.Request) {
		authPath = r.URL.Path
		_, _ = io.WriteString(w, `{"redirect_uri":"`+claudeRedirectURI+`?code=c1"}`)
	})
	mux.HandleFunc("POST /v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"access_token":"at","expires_in":60}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	overrideClaudeURLs(t, srv.URL)

	draft, err := exchangeSessionKey(context.Background(), srv.Client(), "sk-ant-sid01-x")
	if err != nil {
		t.Fatal(err)
	}
	if authPath != "/v1/oauth/org-solo/authorize" {
		t.Fatalf("authorize path = %q", authPath)
	}
	// 响应无 state：draft 仍应正常产出（state 是可选回传）。
	if draft.Credentials["access_token"] != "at" {
		t.Fatalf("draft = %+v", draft.Credentials)
	}
}

// TestExchangeSessionKeyStep1Expired 第 1 步 401：提示 sessionKey 已过期。
func TestExchangeSessionKeyStep1Expired(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/organizations", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid session"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	overrideClaudeURLs(t, srv.URL)

	_, err := exchangeSessionKey(context.Background(), srv.Client(), "sk-ant-sid01-dead")
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeUpstreamAuth {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(ae.Message, "sessionKey 已过期") {
		t.Fatalf("message = %q", ae.Message)
	}
	if !strings.Contains(ae.Message, "401") {
		t.Fatalf("message 缺少状态码: %q", ae.Message)
	}
}

// TestExchangeSessionKeyStep2And3Failure 第 2/3 步失败：错误含状态码与响应片段。
func TestExchangeSessionKeyStep2And3Failure(t *testing.T) {
	t.Run("step2 500", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/organizations", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `[{"uuid":"org-1","raven_type":"team"}]`)
		})
		mux.HandleFunc("POST /v1/oauth/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"upstream exploded with a very long message that should be truncated"}`)
		})
		mux.HandleFunc("POST /v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("step2 失败后不应走到第 3 步")
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		overrideClaudeURLs(t, srv.URL)

		_, err := exchangeSessionKey(context.Background(), srv.Client(), "sk-ant-sid01-x")
		ae := ir.AsAPIError(err)
		if ae == nil || ae.Code != ir.ErrCodeUpstreamFailed {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(ae.Message, "500") || !strings.Contains(ae.Message, "upstream exploded") {
			t.Fatalf("message = %q", ae.Message)
		}
	})

	t.Run("step3 400", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/organizations", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `[{"uuid":"org-1"}]`)
		})
		mux.HandleFunc("POST /v1/oauth/", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"redirect_uri":"`+claudeRedirectURI+`?code=c1&state=s1"}`)
		})
		mux.HandleFunc("POST /v1/oauth/token", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		overrideClaudeURLs(t, srv.URL)

		_, err := exchangeSessionKey(context.Background(), srv.Client(), "sk-ant-sid01-x")
		ae := ir.AsAPIError(err)
		if ae == nil || ae.Code != ir.ErrCodeUpstreamAuth {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(ae.Message, "400") || !strings.Contains(ae.Message, "invalid_grant") {
			t.Fatalf("message = %q", ae.Message)
		}
	})
}

// TestExchangeSessionKeyBadResponses 授权响应缺字段 / 空组织列表的兜底。
func TestExchangeSessionKeyBadResponses(t *testing.T) {
	t.Run("authorize 无 redirect_uri", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/organizations", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `[{"uuid":"org-1"}]`)
		})
		mux.HandleFunc("POST /v1/oauth/", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		overrideClaudeURLs(t, srv.URL)

		if _, err := exchangeSessionKey(context.Background(), srv.Client(), "sk"); err == nil {
			t.Fatal("缺 redirect_uri 应报错")
		}
	})

	t.Run("redirect_uri 无 code", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/organizations", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `[{"uuid":"org-1"}]`)
		})
		mux.HandleFunc("POST /v1/oauth/", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"redirect_uri":"`+claudeRedirectURI+`?state=s1"}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		overrideClaudeURLs(t, srv.URL)

		if _, err := exchangeSessionKey(context.Background(), srv.Client(), "sk"); err == nil {
			t.Fatal("redirect_uri 无 code 应报错")
		}
	})

	t.Run("空组织列表", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/organizations", func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `[]`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		overrideClaudeURLs(t, srv.URL)

		if _, err := exchangeSessionKey(context.Background(), srv.Client(), "sk"); err == nil {
			t.Fatal("空组织列表应报错")
		}
	})
}

// TestChatStreamLazySessionKeyExchange ChatStream 懒处理接入点：
// credentials 只有 session_key 时自动换取并写回，随后正常对话；
// 并发首调只允许一次三步流（互斥锁 + double-check）。
func TestChatStreamLazySessionKeyExchange(t *testing.T) {
	srv, fx := newSessionKeyFixtureServer(t)
	overrideClaudeURLs(t, srv.URL)

	p := &Provider{}
	env := provider.NewAccountEnv("acc-sk", channelID)
	env.SetCredential("session_key", "sk-ant-sid01-live")
	env.SetCredential("base_url", srv.URL)
	env.HTTPClient = srv.Client()

	const concurrency = 4
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
				UpstreamModel: "claude-sonnet-4-6",
				Stream:        false,
				Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "ping"}},
			})
			if err != nil {
				t.Errorf("ChatStream: %v", err)
				return
			}
			for ev := range ch {
				if ev.Type == ir.EventError {
					t.Errorf("事件错误: %v", ev.Err)
				}
			}
		}()
	}
	wg.Wait()

	// 三步流只执行一次；OAuth 凭证已写回 env（后续请求直接用 access_token）。
	if got := fx.orgsSeen(); got != 1 {
		t.Fatalf("组织查询次数 = %d, want 1", got)
	}
	if env.GetCredential("access_token") != "sk-ant-oat01-new" ||
		env.GetCredential("refresh_token") != "sk-ant-ort01-new" ||
		env.GetCredential("expires_at") == "" {
		t.Fatalf("env credentials = %+v", env.Credentials)
	}
	// session_key 原值保留（换来的 token 失效后可再换）。
	if env.GetCredential("session_key") != "sk-ant-sid01-live" {
		t.Fatalf("session_key 被清除: %q", env.GetCredential("session_key"))
	}
}

// TestChatStreamManualTokenBeatsSessionKey 已有 access_token 时不再触发换取。
func TestChatStreamManualTokenBeatsSessionKey(t *testing.T) {
	var orgsHit int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/organizations", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&orgsHit, 1)
		_, _ = io.WriteString(w, `[]`)
	})
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"m1","type":"message","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	overrideClaudeURLs(t, srv.URL)

	p := &Provider{}
	env := provider.NewAccountEnv("acc-manual", channelID)
	env.SetCredential("session_key", "sk-ant-sid01-ignored")
	env.SetCredential("access_token", "manual-token")
	env.SetCredential("base_url", srv.URL)
	env.HTTPClient = srv.Client()

	ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
		UpstreamModel: "claude-sonnet-4-6",
		Stream:        false,
		Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for ev := range ch {
		if ev.Type == ir.EventError {
			t.Fatalf("事件错误: %v", ev.Err)
		}
	}
	if atomic.LoadInt32(&orgsHit) != 0 {
		t.Fatal("已有 access_token 时不应触发 sessionKey 换取")
	}
}

// TestRefreshCredentialSessionKeyOnly 刷新路径接入：只有 session_key 的账号
// 手动刷新时自动换取并返回 draft。
func TestRefreshCredentialSessionKeyOnly(t *testing.T) {
	srv, fx := newSessionKeyFixtureServer(t)
	overrideClaudeURLs(t, srv.URL)

	env := provider.NewAccountEnv("acc-sk-refresh", channelID)
	env.SetCredential("session_key", "sk-ant-sid01-live")
	env.HTTPClient = srv.Client()

	p := &Provider{}
	draft, err := p.RefreshCredential(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Credentials["access_token"] != "sk-ant-oat01-new" {
		t.Fatalf("draft = %+v", draft.Credentials)
	}
	if fx.tokenBody["grant_type"] != "authorization_code" {
		t.Fatalf("token body = %+v", fx.tokenBody)
	}
	if env.GetCredential("access_token") != "sk-ant-oat01-new" {
		t.Fatalf("env = %+v", env.Credentials)
	}
}
