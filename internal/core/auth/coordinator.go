// Package auth 是统一认证中心。
//
// 它把四种形态各异的登录方式收进同一套接口，渠道插件只需要在 Manifest 里
// 声明参数，不需要自己写 HTTP 监听、不需要自己写轮询循环：
//
//  1. PKCE 本地回环 —— 起临时 HTTP 服务拦授权码，用完即关
//  2. 设备码轮询    —— 拿 user_code 给用户看，后台按 interval 轮询
//  3. 本机凭证导入 —— 读客户端已登录的配置文件
//  4. 手动粘贴     —— 无需流程，直接入库
//
// 前两种是真正复杂的地方：端口冲突、state 校验、超时、轮询间隔、错误码重试，
// 集中实现一次，所有渠道都受益。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/provider"
)

// Status 是一次认证会话的状态。
type Status string

const (
	StatusPending Status = "pending"
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
	StatusExpired Status = "expired"
)

// Session 是一次进行中的认证。
type Session struct {
	ID         string              `json:"id"`
	ProviderID string              `json:"provider_id"`
	Method     provider.AuthMethod `json:"method"`
	Status     Status              `json:"status"`

	// --- PKCE ---
	// AuthorizeURL 前端需要打开这个地址。
	AuthorizeURL string `json:"authorize_url,omitempty"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	State        string `json:"state,omitempty"`
	CodeVerifier string `json:"-"`
	// oauthCfg 是本次 PKCE 流程的参数；不对外序列化，手动粘贴回调地址时
	// 仍要按同一套 token 端点换 token。
	oauthCfg *provider.OAuthConfig

	// --- 设备码 ---
	UserCode        string `json:"user_code,omitempty"`
	VerificationURI string `json:"verification_uri,omitempty"`
	Interval        int    `json:"interval,omitempty"`

	// --- 结果 ---
	Credential *provider.CredentialDraft `json:"credential,omitempty"`
	Err        string                    `json:"error,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Coordinator 管理全部认证会话。
type Coordinator struct {
	mu       sync.Mutex
	sessions map[string]*Session

	// listeners 记录 PKCE 流程占用的本地监听，成功后关闭。
	listeners map[string]net.Listener

	HTTPClient *http.Client
	// CallbackPort PKCE 回调端口。0 表示随机（部分 OAuth 服务要求预注册固定端口）。
	CallbackPort int
	// Timeout 单次认证会话的最长存活时间。
	Timeout time.Duration
}

// New 创建协调器。
func New() *Coordinator {
	c := &Coordinator{
		sessions:  make(map[string]*Session, 8),
		listeners: make(map[string]net.Listener, 4),
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		CallbackPort: 54321,
		Timeout:      10 * time.Minute,
	}
	go c.gcLoop()
	return c
}

// gcLoop 清理过期会话并释放残留监听。
func (c *Coordinator) gcLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		c.mu.Lock()
		for id, s := range c.sessions {
			if s.Status != StatusPending {
				// 非 pending 的会话保留 5 分钟供前端查询结果
				if now.Sub(s.CreatedAt) > 5*time.Minute {
					c.closeListenerLocked(id)
					delete(c.sessions, id)
				}
				continue
			}
			if now.After(s.ExpiresAt) {
				s.Status = StatusExpired
				s.Err = "认证超时"
				c.closeListenerLocked(id)
			}
		}
		c.mu.Unlock()
	}
}

func (c *Coordinator) closeListenerLocked(id string) {
	if ln, ok := c.listeners[id]; ok {
		_ = ln.Close()
		delete(c.listeners, id)
	}
}

// Get 查询会话。
func (c *Coordinator) Get(id string) *Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.sessions[id]; ok {
		cp := *s
		return &cp
	}
	return nil
}

// List 返回全部会话（供 UI 展示进行中的认证）。
func (c *Coordinator) List() []*Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*Session, 0, len(c.sessions))
	for _, s := range c.sessions {
		cp := *s
		out = append(out, &cp)
	}
	return out
}

// Cancel 取消一个会话。
func (c *Coordinator) Cancel(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.sessions[id]; ok && s.Status == StatusPending {
		s.Status = StatusFailed
		s.Err = "用户取消"
	}
	c.closeListenerLocked(id)
}

// register 保存新会话。
func (c *Coordinator) register(s *Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[s.ID] = s
}

// update 更新会话（调用方持有修改后的副本）。
func (c *Coordinator) update(s *Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[s.ID] = s
}

// --- PKCE ---

// StartPKCE 启动授权码 + PKCE 流程。
//
// 返回值里的 AuthorizeURL 需要用户在浏览器打开；本方法已经起好了本地回调监听。
func (c *Coordinator) StartPKCE(providerID string, cfg *provider.OAuthConfig) (*Session, error) {
	if cfg == nil || cfg.AuthorizeURL == "" || cfg.TokenURL == "" || cfg.ClientID == "" {
		return nil, errors.New("auth: PKCE 需要 authorize_url / token_url / client_id")
	}

	redirectPath := cfg.RedirectPath
	if redirectPath == "" {
		redirectPath = "/oauth/callback"
	}

	// 先占端口，才能知道 redirect_uri 该写什么。
	redirectHost := cfg.RedirectHost
	if redirectHost == "" {
		redirectHost = "127.0.0.1"
	}
	fixedPort := cfg.RedirectPort
	// 显式声明 RedirectPort 的客户端（如 Claude Code）预注册了 redirect_uri，
	// 端口不能变；未声明时沿用旧的随机回退行为。
	strictPort := fixedPort != 0
	if !strictPort {
		fixedPort = c.CallbackPort
	}
	addr := fmt.Sprintf("127.0.0.1:%d", fixedPort)
	if fixedPort == 0 {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if strictPort {
			return nil, fmt.Errorf("auth: 回调端口 %d 被占用: %w", fixedPort, err)
		}
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("auth: 无法监听本地回调端口: %w", err)
		}
	}

	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://%s:%d%s", redirectHost, port, redirectPath)

	verifier, err := randomURLSafe(64)
	if err != nil {
		ln.Close()
		return nil, err
	}
	challenge := pkceChallenge(verifier)
	state, err := randomURLSafe(24)
	if err != nil {
		ln.Close()
		return nil, err
	}

	s := &Session{
		ID:           newSessionID(),
		ProviderID:   providerID,
		Method:       provider.AuthOAuthPKCE,
		Status:       StatusPending,
		RedirectURI:  redirectURI,
		State:        state,
		CodeVerifier: verifier,
		oauthCfg:     cfg,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(c.Timeout),
	}
	s.AuthorizeURL = buildAuthorizeURL(cfg, redirectURI, challenge, state)

	c.mu.Lock()
	c.sessions[s.ID] = s
	c.listeners[s.ID] = ln
	c.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc(redirectPath, func(w http.ResponseWriter, r *http.Request) {
		c.handlePKCECallback(w, r, s, cfg)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		_ = srv.Serve(ln)
	}()

	return s, nil
}

// SubmitPKCECallback 处理“用户手动粘贴回调地址”的授权码。
//
// 浏览器打不开本机回调、或回调页被别的浏览器接管时，用户可以复制完整
// 回调地址（含 code/state）回来完成同一套换 token 流程。state 存在时
// 必须匹配；只粘贴 code 时按用户主动操作放行。
func (c *Coordinator) SubmitPKCECallback(id, raw string) error {
	c.mu.Lock()
	s := c.sessions[id]
	if s == nil {
		c.mu.Unlock()
		return errors.New("会话不存在或已过期")
	}
	if s.Method != provider.AuthOAuthPKCE || s.oauthCfg == nil {
		c.mu.Unlock()
		return errors.New("该会话不是浏览器授权流程")
	}
	if s.Status == StatusSuccess {
		c.mu.Unlock()
		return nil
	}
	if s.Status != StatusPending {
		c.mu.Unlock()
		return fmt.Errorf("授权会话已失效：%s", s.Status)
	}
	cfg := s.oauthCfg
	c.mu.Unlock()

	code, state, err := parseManualCallback(raw)
	if err != nil {
		return err
	}
	if state != "" && state != s.State {
		return errors.New("state 校验失败（回调地址与本次授权不匹配）")
	}
	cred, err := c.exchangeCode(s, cfg, code)
	if err != nil {
		c.finish(s, nil, err)
		return err
	}
	c.finish(s, cred, nil)
	return nil
}

// parseManualCallback 从完整回调 URL / 查询串 / 裸 code 里取 code 与 state。
func parseManualCallback(raw string) (code, state string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("请粘贴回调地址或授权码")
	}
	var q url.Values
	switch {
	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
		u, perr := url.Parse(raw)
		if perr != nil {
			return "", "", fmt.Errorf("回调地址不合法: %w", perr)
		}
		q = u.Query()
	case strings.HasPrefix(raw, "?"):
		q, err = url.ParseQuery(raw[1:])
	case strings.Contains(raw, "code="):
		q, err = url.ParseQuery(strings.TrimPrefix(raw, "?"))
	default:
		return raw, "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("回调地址参数不合法: %w", err)
	}
	if e := q.Get("error"); e != "" {
		return "", "", fmt.Errorf("授权被拒绝: %s %s", e, q.Get("error_description"))
	}
	code = strings.TrimSpace(q.Get("code"))
	if code == "" {
		return "", "", errors.New("回调地址里没有 code")
	}
	return code, strings.TrimSpace(q.Get("state")), nil
}

// handlePKCECallback 处理浏览器回调。
func (c *Coordinator) handlePKCECallback(w http.ResponseWriter, r *http.Request, s *Session, cfg *provider.OAuthConfig) {
	q := r.URL.Query()

	// state 校验：防 CSRF，这一步不能省。
	if got := q.Get("state"); got != s.State {
		c.finish(s, nil, errors.New("state 校验失败（可能是伪造的回调）"))
		writeCallbackPage(w, false, "授权失败：state 不匹配")
		return
	}
	if e := q.Get("error"); e != "" {
		desc := q.Get("error_description")
		c.finish(s, nil, fmt.Errorf("授权被拒绝: %s %s", e, desc))
		writeCallbackPage(w, false, "授权被拒绝："+e)
		return
	}
	code := q.Get("code")
	if code == "" {
		c.finish(s, nil, errors.New("回调里没有 code"))
		writeCallbackPage(w, false, "授权失败：没有收到 code")
		return
	}

	cred, err := c.exchangeCode(s, cfg, code)
	if err != nil {
		c.finish(s, nil, err)
		writeCallbackPage(w, false, "换取 token 失败："+err.Error())
		return
	}
	c.finish(s, cred, nil)
	writeCallbackPage(w, true, "授权成功，可以关闭此页面回到 Aether Cockpit")
}

// exchangeCode 用授权码换 token。
func (c *Coordinator) exchangeCode(s *Session, cfg *provider.OAuthConfig, code string) (*provider.CredentialDraft, error) {
	reqBody := ""
	contentType := "application/x-www-form-urlencoded"
	if cfg.TokenFormat == "json" {
		payload := map[string]string{
			"grant_type":    "authorization_code",
			"code":          code,
			"client_id":     cfg.ClientID,
			"redirect_uri":  s.RedirectURI,
			"code_verifier": s.CodeVerifier,
			"state":         s.State,
		}
		if cfg.ClientSecret != "" {
			payload["client_secret"] = cfg.ClientSecret
		}
		for k, v := range cfg.ExtraTokenParams {
			payload[k] = v
		}
		raw, _ := json.Marshal(payload)
		reqBody = string(raw)
		contentType = "application/json"
	} else {
		form := url.Values{}
		form.Set("grant_type", "authorization_code")
		form.Set("code", code)
		form.Set("client_id", cfg.ClientID)
		form.Set("redirect_uri", s.RedirectURI)
		form.Set("code_verifier", s.CodeVerifier)
		if cfg.ClientSecret != "" {
			form.Set("client_secret", cfg.ClientSecret)
		}
		for k, v := range cfg.ExtraTokenParams {
			form.Set(k, v)
		}
		reqBody = form.Encode()
	}

	req, err := http.NewRequest(http.MethodPost, cfg.TokenURL, strings.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 token 端点失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token 端点返回 %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	return parseTokenResponse(body, cfg)
}

// finish 结束会话并释放监听。
func (c *Coordinator) finish(s *Session, cred *provider.CredentialDraft, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if s.Status != StatusPending {
		return
	}
	if err != nil {
		s.Status = StatusFailed
		s.Err = err.Error()
	} else {
		s.Status = StatusSuccess
		s.Credential = cred
	}
	c.closeListenerLocked(s.ID)
}

// pkceChallenge 计算 S256 challenge。
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// buildAuthorizeURL 拼接授权地址。
func buildAuthorizeURL(cfg *provider.OAuthConfig, redirectURI, challenge, state string) string {
	u, err := url.Parse(cfg.AuthorizeURL)
	if err != nil {
		return cfg.AuthorizeURL
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	if len(cfg.Scopes) > 0 {
		q.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	for k, v := range cfg.ExtraAuthParams {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// parseTokenResponse 把 token 端点响应转成凭证草稿。
func parseTokenResponse(body []byte, cfg *provider.OAuthConfig) (*provider.CredentialDraft, error) {
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
		// 有些服务把 token 包在 data 里
		Data *struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			IDToken      string `json:"id_token"`
			ExpiresIn    int    `json:"expires_in"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("token 响应不是合法 JSON: %w", err)
	}
	if tok.AccessToken == "" && tok.Data != nil {
		tok.AccessToken = tok.Data.AccessToken
		tok.RefreshToken = tok.Data.RefreshToken
		tok.IDToken = tok.Data.IDToken
		tok.ExpiresIn = tok.Data.ExpiresIn
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("token 响应里没有 access_token: %s", truncate(string(body), 200))
	}

	tokenField := cfg.TokenField
	if tokenField == "" {
		tokenField = "access_token"
	}
	refreshField := cfg.RefreshField
	if refreshField == "" {
		refreshField = "refresh_token"
	}
	expiresField := cfg.ExpiresField
	if expiresField == "" {
		expiresField = "expires_at"
	}

	creds := map[string]string{tokenField: tok.AccessToken}
	if tok.RefreshToken != "" {
		creds[refreshField] = tok.RefreshToken
	}
	if tok.IDToken != "" {
		creds["id_token"] = tok.IDToken
		for field, path := range cfg.IDTokenClaims {
			if field == "" || path == "" {
				continue
			}
			if v := claimString(tok.IDToken, path); v != "" {
				creds[field] = v
			}
		}
	}
	for k, v := range cfg.ExtraCredentials {
		creds[k] = v
	}

	var expiresAt int64
	if tok.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).Unix()
		creds[expiresField] = strconv.FormatInt(expiresAt, 10)
	}

	return &provider.CredentialDraft{
		Label:       "oauth-" + shortLabel(tok.AccessToken),
		Credentials: creds,
		ExpiresAt:   expiresAt,
		Source:      "oauth_pkce",
	}, nil
}

// claimString 从 id_token 里按 `顶层键#嵌套路径` 取字符串声明。
// 不校验签名：令牌刚由 token 端点返回，只用于取标识。
func claimString(idToken, path string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	top, rest, _ := strings.Cut(path, "#")
	var cur any = claims[top]
	for _, seg := range strings.Split(rest, ".") {
		if seg == "" {
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[seg]
	}
	switch v := cur.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return ""
	}
}

// --- 工具 ---

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func newSessionID() string {
	s, _ := randomURLSafe(12)
	return "auth_" + s
}

func shortLabel(token string) string {
	if len(token) <= 8 {
		return token
	}
	return token[:8]
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// writeCallbackPage 返回给浏览器一个可读的结果页。
func writeCallbackPage(w http.ResponseWriter, ok bool, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	color := "#34d399"
	icon := "✓"
	if !ok {
		color = "#f87171"
		icon = "×"
	}
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Aether Cockpit</title>
<style>body{background:#0a0c10;color:#e8ebf1;font-family:-apple-system,"Segoe UI",sans-serif;
display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
.box{text-align:center}.icon{font-size:48px;color:%s;margin-bottom:16px}
.msg{font-size:15px;line-height:1.6;max-width:420px}</style></head>
<body><div class="box"><div class="icon">%s</div><div class="msg">%s</div></div></body></html>`,
		color, icon, msg)
}
