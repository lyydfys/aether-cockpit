package kiro

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/provider"
)

// Kiro 登录：app.kiro.dev/signin（PKCE）+ 本地回调 + Kiro oauth/token 交换。
// 参考 local-vault kiro_oauth；portal 要求回调端口在白名单内。

var (
	kiroPortalURL = "https://app.kiro.dev/signin"
	kiroTokenURL  = "https://prod.us-east-1.auth.desktop.kiro.dev/oauth/token"
)

const kiroLoginTimeout = 10 * time.Minute

var kiroPorts = []int{3128, 4649, 6588, 8008, 9091, 49153, 50153, 51153, 52153, 53153}

// LoginSession 是一次进行中的 Kiro 登录。
type LoginSession struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	LoginURL   string `json:"login_url"`
	State      string `json:"state"`
	Err        string `json:"error,omitempty"`
	Interval   int    `json:"interval"`
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  int64  `json:"expires_at"`
	Account    string `json:"account,omitempty"`

	port     int
	stateTok string
	verifier string
	cbPath   string

	mu        *sync.Mutex
	ln        net.Listener
	srv       *http.Server
	timer     *time.Timer
	access    string
	refresh   string
	profileAR string
	region    string
	loginOpt  string
	errText   string
	committed bool
}

var (
	kiroMu       sync.Mutex
	kiroSessions = map[string]*LoginSession{}
)

func randomB64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// StartLogin 选择回调端口、启动监听并生成 portal URL。
func StartLogin(providerID string) (*LoginSession, error) {
	if _, ok := provider.Get(providerID); !ok || (providerID != "kiro") {
		return nil, fmt.Errorf("渠道 %s 不是 Kiro", providerID)
	}
	var ln net.Listener
	port := 0
	for _, p := range kiroPorts {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			ln = l
			port = p
			break
		}
	}
	if ln == nil {
		return nil, fmt.Errorf("Kiro 回调端口均被占用（3128/4649/6588/8008/9091/49153…）")
	}
	verifier := randomB64(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := randomB64(16)
	cbBase := fmt.Sprintf("http://localhost:%d", port)
	cbPath := "/oauth/callback"
	redirectURI := cbBase + cbPath

	u, err := url.Parse(kiroPortalURL)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	q := u.Query()
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("redirect_uri", redirectURI)
	q.Set("redirect_from", "KiroIDE")
	u.RawQuery = q.Encode()

	s := &LoginSession{
		ID:         "kiro_" + randomB64(8),
		ProviderID: providerID,
		LoginURL:   u.String(),
		State:      "pending",
		Interval:   1,
		CreatedAt:  time.Now().Unix(),
		ExpiresAt:  time.Now().Add(kiroLoginTimeout).Unix(),
		port:       port,
		stateTok:   state,
		verifier:   verifier,
		cbPath:     cbPath,
		mu:         &sync.Mutex{},
		ln:         ln,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.handleCallback(w, r)
	})
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	s.timer = time.AfterFunc(kiroLoginTimeout, func() {
		s.mu.Lock()
		if s.State == "pending" {
			s.State = "expired"
			s.Err = "登录超时"
		}
		s.mu.Unlock()
		s.stop()
	})

	kiroMu.Lock()
	kiroSessions[s.ID] = s
	kiroMu.Unlock()
	return s, nil
}

func (s *LoginSession) stop() {
	if s.ln != nil {
		_ = s.ln.Close()
	}
	if s.timer != nil {
		s.timer.Stop()
	}
}

func (s *LoginSession) handleCallback(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path != "/oauth/callback" && path != "/signin/callback" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.fail("授权失败: " + e)
		_, _ = w.Write([]byte("<html><body>Kiro 授权失败</body></html>"))
		return
	}
	if q.Get("state") != s.stateTok {
		s.fail("授权状态校验失败，请重新发起登录")
		_, _ = w.Write([]byte("<html><body>Kiro 授权失败：state 不匹配</body></html>"))
		return
	}
	code := strings.TrimSpace(q.Get("code"))
	if code == "" {
		s.fail("Kiro 回调缺少 code")
		_, _ = w.Write([]byte("<html><body>Kiro 授权失败：缺少 code</body></html>"))
		return
	}
	loginOption := strings.ToLower(strings.TrimSpace(firstOf(q.Get("login_option"), q.Get("loginOption"))))
	region := firstOf(q.Get("idc_region"), q.Get("idcRegion"))
	redirect := fmt.Sprintf("http://localhost:%d%s?login_option=%s", s.port, path, url.QueryEscape(loginOption))

	tok, err := exchangeKiroCode(code, s.verifier, redirect)
	if err != nil {
		s.fail(err.Error())
		_, _ = w.Write([]byte("<html><body>Kiro 授权失败：token 交换失败</body></html>"))
		return
	}
	access := pickStr(tok, "accessToken", "access_token")
	if access == "" {
		s.fail("Kiro token 响应缺少 access token")
		_, _ = w.Write([]byte("<html><body>Kiro 授权失败：缺少 token</body></html>"))
		return
	}
	s.mu.Lock()
	if s.State == "pending" {
		s.access = access
		s.refresh = pickStr(tok, "refreshToken", "refresh_token")
		s.profileAR = pickStr(tok, "profileArn", "profile_arn")
		s.region = pickStr(tok, "region")
		if s.region == "" {
			s.region = region
		}
		if s.region == "" {
			s.region = "us-east-1"
		}
		s.loginOpt = loginOption
		s.State = "done"
	}
	s.mu.Unlock()
	s.stop()
	_, _ = w.Write([]byte("<html><body style='font-family:sans-serif;text-align:center;padding:32px'><h2>Kiro 登录已完成</h2><p>可以关闭此页面返回 Aether Cockpit。</p></body></html>"))
}

func (s *LoginSession) fail(msg string) {
	s.mu.Lock()
	if s.State == "pending" {
		s.State = "failed"
		s.Err = msg
		s.errText = msg
	}
	s.mu.Unlock()
	s.stop()
}

func exchangeKiroCode(code, verifier, redirect string) (map[string]any, error) {
	body, _ := json.Marshal(map[string]any{
		"code": code, "code_verifier": verifier, "redirect_uri": redirect,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, kiroTokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 Kiro oauth/token 失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Kiro oauth/token 返回 %d", resp.StatusCode)
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil, fmt.Errorf("Kiro token 响应不是合法 JSON")
	}
	if data, ok := root["data"].(map[string]any); ok {
		return data, nil
	}
	return root, nil
}

func pickStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstOf(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// GetLogin 返回会话副本。
func GetLogin(id string) *LoginSession {
	kiroMu.Lock()
	s := kiroSessions[id]
	kiroMu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *s
	cp.mu = nil
	cp.ln = nil
	cp.srv = nil
	cp.timer = nil
	return &cp
}

// CancelLogin 取消并关闭监听。
func CancelLogin(id string) {
	kiroMu.Lock()
	s := kiroSessions[id]
	kiroMu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.State == "pending" {
		s.State = "failed"
		s.Err = "用户取消"
	}
	s.mu.Unlock()
	s.stop()
}

// MarkCommitted 标记账号已创建。
func MarkCommitted(id, accountID string) bool {
	kiroMu.Lock()
	s := kiroSessions[id]
	kiroMu.Unlock()
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State != "done" || s.Account != "" {
		return false
	}
	s.Account = accountID
	return true
}

// Credentials 返回完成后的凭证。
func Credentials(id string) map[string]string {
	kiroMu.Lock()
	s := kiroSessions[id]
	kiroMu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State != "done" || s.access == "" {
		return nil
	}
	creds := map[string]string{
		"access_token": s.access,
		"region":       s.region,
		"base_url":     "https://q." + s.region + ".amazonaws.com",
	}
	if s.refresh != "" {
		creds["refresh_token"] = s.refresh
	}
	if s.profileAR != "" {
		creds["profile_arn"] = s.profileAR
	}
	if s.loginOpt != "" {
		creds["login_option"] = s.loginOpt
	}
	return creds
}
