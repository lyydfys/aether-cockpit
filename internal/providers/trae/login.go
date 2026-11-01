package trae

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Trae 登录：/authorization（PKCE，本地回调）→ ExchangeToken。
// 参考 local-vault trae_oauth；设备信息按官方客户端字段生成后随账号保存。

var traeLoginHosts = map[string]string{
	"trae-cn":      "https://www.trae.cn",
	"trae-intl":    "https://www.trae.ai",
	"trae-solo-cn": "https://www.trae.cn",
	"trae-solo":    "https://www.trae.ai",
}

var traeAccountOrigins = map[string]string{
	"trae-cn":      "https://api.trae.cn",
	"trae-intl":    "https://grow-normal.trae.ai",
	"trae-solo-cn": "https://api.trae.cn",
	"trae-solo":    "https://grow-normal.trae.ai",
}

const (
	traeClientID     = "ono9krqynydwx5"
	traeSoloClientID = "en1oxy7wnw8j9n"
	traeLoginTimeout = 10 * time.Minute
)

func traeClientIDFor(providerID string) string {
	if strings.Contains(providerID, "solo") {
		return traeSoloClientID
	}
	return traeClientID
}

// LoginSession 是一次进行中的 Trae 登录。
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

	mu        *sync.Mutex
	ln        net.Listener
	srv       *http.Server
	timer     *time.Timer
	verifier  string
	cbURL     string
	machineID string
	deviceID  string
	token     string
	refresh   string
	userID    string
	host      string
}

var (
	traeMu       sync.Mutex
	traeSessions = map[string]*LoginSession{}
)

func randB64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// StartLogin 启动一次 Trae 登录。
func StartLogin(providerID string) (*LoginSession, error) {
	host := traeLoginHosts[providerID]
	if host == "" {
		return nil, fmt.Errorf("渠道 %s 不是 Trae", providerID)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("分配 Trae 回调端口失败: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	cbURL := fmt.Sprintf("http://127.0.0.1:%d/authorize", port)
	verifier := randB64(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	machineID := randHex(16)
	deviceID := randHex(16)
	trace := randHex(16)

	u, _ := url.Parse(host + "/authorization")
	q := u.Query()
	q.Set("login_version", "1")
	q.Set("auth_from", "trae")
	q.Set("login_channel", "native_ide")
	q.Set("plugin_version", "local")
	q.Set("auth_type", "local")
	q.Set("client_id", traeClientIDFor(providerID))
	q.Set("redirect", "0")
	q.Set("login_trace_id", trace)
	q.Set("auth_callback_url", cbURL)
	q.Set("machine_id", machineID)
	q.Set("device_id", deviceID)
	q.Set("x_device_id", deviceID)
	q.Set("x_machine_id", machineID)
	q.Set("x_device_brand", "Windows")
	q.Set("x_device_type", "windows")
	q.Set("x_os_version", osVersion)
	q.Set("x_env", "")
	q.Set("x_app_version", "3.5.54")
	q.Set("x_app_type", "stable")
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()

	s := &LoginSession{
		ID:         "trae_" + randHex(8),
		ProviderID: providerID,
		LoginURL:   u.String(),
		State:      "pending",
		Interval:   1,
		CreatedAt:  time.Now().Unix(),
		ExpiresAt:  time.Now().Add(traeLoginTimeout).Unix(),
		mu:         &sync.Mutex{},
		ln:         ln,
		verifier:   verifier,
		cbURL:      cbURL,
		machineID:  machineID,
		deviceID:   deviceID,
		host:       host,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.handleCallback(w, r)
	})
	s.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.srv.Serve(ln) }()
	s.timer = time.AfterFunc(traeLoginTimeout, func() {
		s.mu.Lock()
		if s.State == "pending" {
			s.State = "expired"
			s.Err = "登录超时"
		}
		s.mu.Unlock()
		s.stop()
	})
	traeMu.Lock()
	traeSessions[s.ID] = s
	traeMu.Unlock()
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
	code := strings.TrimSpace(q.Get("code"))
	if code == "" {
		code = strings.TrimSpace(q.Get("authCode"))
	}
	if code == "" {
		if raw := q.Get("authCodeInfo"); raw != "" {
			if dec, err := url.QueryUnescape(raw); err == nil {
				raw = dec
			}
			var info map[string]any
			if json.Unmarshal([]byte(raw), &info) == nil {
				code = strField(info, "authCode", "auth_code")
				if c, ok := info["result"].(map[string]any); ok && code == "" {
					code = strField(c, "authCode", "auth_code")
				}
			}
		}
	}
	if code == "" {
		s.fail("Trae 回调缺少 auth code")
		_, _ = w.Write([]byte("<html><body>Trae 授权失败：缺少 auth code</body></html>"))
		return
	}
	tok, userID, err := s.exchange(code)
	if err != nil {
		s.fail(err.Error())
		_, _ = w.Write([]byte("<html><body>Trae 授权失败：token 交换失败</body></html>"))
		return
	}
	s.mu.Lock()
	if s.State == "pending" {
		s.token = tok
		s.refresh = ""
		s.userID = userID
		s.State = "done"
	}
	s.mu.Unlock()
	s.stop()
	_, _ = w.Write([]byte("<html><body style='font-family:sans-serif;text-align:center;padding:32px'><h2>Trae 登录已完成</h2><p>可以关闭此页面返回 Aether Cockpit。</p></body></html>"))
}

func (s *LoginSession) exchange(code string) (string, string, error) {
	origin := traeAccountOrigins[s.ProviderID]
	body, _ := json.Marshal(map[string]any{
		"ClientID":     traeClientIDFor(s.ProviderID),
		"AuthCode":     code,
		"CodeVerifier": s.verifier,
		"DeviceInfo": map[string]any{
			"DeviceID": s.deviceID, "MachineID": s.machineID, "PlatformCode": "IDE_PC",
			"DeviceType": "PC", "DeviceName": os.Getenv("USERNAME"), "DeviceModel": "PC",
			"ClientVersion": "3.5.54", "DevicePublicKey": "", "DeviceBrand": "Microsoft",
			"DeviceCPU": "", "OSInfo": "windows", "OSVersion": osVersion,
		},
		"IDEVersion": "3.5.54",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	u := origin + "/cloudide/api/v3/trae/oauth/ExchangeToken"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-cloudide-token", "")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("请求 Trae ExchangeToken 失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("Trae ExchangeToken 返回 %d", resp.StatusCode)
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return "", "", fmt.Errorf("Trae ExchangeToken 响应不是合法 JSON")
	}
	token := findString(root, "AccessToken", "accessToken", "access_token", "Token", "token")
	if token == "" {
		return "", "", fmt.Errorf("Trae ExchangeToken 响应缺少 access token")
	}
	userID := findString(root, "UserID", "userId", "user_id", "UID", "uid")
	return token, userID, nil
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

func findString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, v := range m {
		if child, ok := v.(map[string]any); ok {
			if got := findString(child, keys...); got != "" {
				return got
			}
		}
	}
	return ""
}

func strField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// GetLogin 返回会话副本。
func GetLogin(id string) *LoginSession {
	traeMu.Lock()
	s := traeSessions[id]
	traeMu.Unlock()
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
	traeMu.Lock()
	s := traeSessions[id]
	traeMu.Unlock()
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
	traeMu.Lock()
	s := traeSessions[id]
	traeMu.Unlock()
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
	traeMu.Lock()
	s := traeSessions[id]
	traeMu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.State != "done" || s.token == "" {
		return nil
	}
	region := "SG"
	if s.ProviderID == "trae-cn" {
		region = "CN"
	}
	creds := map[string]string{
		"token":      s.token,
		"region":     region,
		"machine_id": s.machineID,
		"device_id":  s.deviceID,
		"host":       s.host,
	}
	if s.refresh != "" {
		creds["refresh_token"] = s.refresh
	}
	if s.userID != "" {
		creds["user_id"] = s.userID
	}
	return creds
}
