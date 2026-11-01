package zed

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Zed 官方 native_app_signin：本地生成 RSA 密钥并监听回环端口，
// 授权后浏览器把加密 access_token 回调回来，本机解密。
// 参考 cockpit-tools zed_oauth。

var zedSigninURL = "https://zed.dev/native_app_signin"

const zedLoginTimeout = 10 * time.Minute

// LoginSession 是一次进行中的 Zed 登录。
type LoginSession struct {
	ID        string `json:"id"`
	LoginURL  string `json:"login_url"`
	State     string `json:"state"`
	Err       string `json:"error,omitempty"`
	Interval  int    `json:"interval"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	Account   string `json:"account,omitempty"`
	UserID    string `json:"user_id,omitempty"`

	priv   *rsa.PrivateKey
	ln     net.Listener
	srv    *http.Server
	token  string
	done   bool
	mu     *sync.Mutex
	timer  *time.Timer
	cancel bool
}

var (
	zedMu       sync.Mutex
	zedSessions = map[string]*LoginSession{}
)

func randomID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func decodeB64(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}

// StartLogin 生成密钥、监听回环端口并返回登录页地址。
func StartLogin() (*LoginSession, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("生成 Zed RSA 密钥失败: %w", err)
	}
	pubDER := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("监听 Zed 回调端口失败: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	s := &LoginSession{
		mu:        &sync.Mutex{},
		ID:        "zed_" + randomID(8),
		LoginURL:  fmt.Sprintf("%s?native_app_port=%d&native_app_public_key=%s", zedSigninURL, port, b64url(pubDER)),
		State:     "pending",
		Interval:  1,
		CreatedAt: time.Now().Unix(),
		ExpiresAt: time.Now().Add(zedLoginTimeout).Unix(),
		priv:      key,
		ln:        ln,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.handleCallback(w, r)
	})
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	s.timer = time.AfterFunc(zedLoginTimeout, func() {
		s.mu.Lock()
		if s.State == "pending" {
			s.State = "expired"
			s.Err = "登录超时"
		}
		s.mu.Unlock()
		s.stop()
	})

	zedMu.Lock()
	zedSessions[s.ID] = s
	zedMu.Unlock()
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
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.fail("授权失败: " + e)
		_, _ = w.Write([]byte("<html><body>Zed 授权失败</body></html>"))
		return
	}
	userID := strings.TrimSpace(q.Get("user_id"))
	enc := strings.TrimSpace(q.Get("access_token"))
	if userID == "" || enc == "" {
		s.fail("Zed 回调缺少 user_id/access_token")
		_, _ = w.Write([]byte("<html><body>Zed 授权失败：缺少参数</body></html>"))
		return
	}
	plain, err := decryptToken(s.priv, enc)
	if err != nil {
		s.fail("解密 Zed access_token 失败: " + err.Error())
		_, _ = w.Write([]byte("<html><body>Zed 授权失败：令牌解密失败</body></html>"))
		return
	}
	s.mu.Lock()
	if s.State == "pending" {
		s.UserID = userID
		s.token = plain
		s.State = "done"
		s.done = true
	}
	s.mu.Unlock()
	s.stop()
	_, _ = w.Write([]byte("<html><body style='font-family:sans-serif;text-align:center;padding:32px'><h2>Zed 登录已完成</h2><p>可以关闭此页面返回 Aether Cockpit。</p></body></html>"))
}

func (s *LoginSession) fail(msg string) {
	s.mu.Lock()
	if s.State == "pending" {
		s.State = "failed"
		s.Err = msg
	}
	s.mu.Unlock()
	s.stop()
}

func decryptToken(key *rsa.PrivateKey, encrypted string) (string, error) {
	raw, err := decodeB64(encrypted)
	if err != nil {
		return "", err
	}
	if plain, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, key, raw, nil); err == nil {
		return string(plain), nil
	}
	// 兼容旧客户端：PKCS#1 v1.5。
	plain, err := rsa.DecryptPKCS1v15(rand.Reader, key, raw)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// GetLogin 返回会话副本（不含私钥）。
func GetLogin(id string) *LoginSession {
	zedMu.Lock()
	defer zedMu.Unlock()
	s := zedSessions[id]
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *s
	cp.priv = nil
	cp.ln = nil
	cp.srv = nil
	cp.timer = nil
	return &cp
}

// CancelLogin 取消会话并关闭监听。
func CancelLogin(id string) {
	zedMu.Lock()
	s := zedSessions[id]
	zedMu.Unlock()
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
	zedMu.Lock()
	defer zedMu.Unlock()
	s := zedSessions[id]
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State != "done" || s.done == false || s.Account != "" {
		return false
	}
	s.Account = accountID
	return true
}

// Credentials 返回完成后的凭证。
func Credentials(id string) map[string]string {
	zedMu.Lock()
	s := zedSessions[id]
	zedMu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State != "done" || s.token == "" {
		return nil
	}
	return map[string]string{"user_id": s.UserID, "access_token": s.token}
}
