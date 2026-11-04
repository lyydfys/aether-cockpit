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
	// callbackPath 是本机回环服务唯一接受的路径。
	// 之前 mux 把 "/" 上的一切请求都交给回调处理：用户把回调地址当登录页打开、
	// 或浏览器带 isRedirect 回来时，只会得到"缺少 auth code"，既误导也掩盖了
	// "浏览器流程需要 Trae 客户端"这个事实。
	callbackPath = "/authorize"
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
	cbURL := fmt.Sprintf("http://127.0.0.1:%d%s", port, callbackPath)
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
	// 只认回调路径；其它路径明确返回说明页，不再被当成回调。
	mux.HandleFunc(callbackPath, s.handleCallback)
	mux.HandleFunc("/", s.handleNotCallback)
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
		// 两种"没有 code"分开说，避免统一一句"缺少 auth code"误导：
		//  1) 官方页面带 isRedirect 回来 = 它把 Web 流程交给了 Trae 客户端，
		//     而浏览器/手机上没人回传 auth code；手机端注定完不成。
		//  2) 回调地址被直接打开 = 没有参数，提示用户走正确入口。
		var msg string
		if q.Has("isRedirect") {
			msg = "Trae 授权需要 Trae 客户端回传授权码；浏览器只收到 isRedirect，手机端无法完成。请改用「手动 Token」接入。"
		} else {
			msg = "Trae 回调缺少 auth code：请从 Aether Cockpit 的「打开登录页面」进入官方授权页；手机端没有 Trae 客户端时请改用「手动 Token」。"
		}
		s.fail(msg)
		writeLoginPage(w, http.StatusOK, "Trae 授权未完成", msg)
		return
	}
	tok, userID, err := s.exchange(code)
	if err != nil {
		s.fail(err.Error())
		writeLoginPage(w, http.StatusOK, "Trae 授权失败", "token 交换失败："+err.Error())
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
	writeLoginPage(w, http.StatusOK, "Trae 登录已完成", "可以关闭此页面返回 Aether Cockpit。")
}

// handleNotCallback 处理"打错/直接打开"的路径：明确说清这里是回调服务，
// 而不是登录页（旧实现把 "/" 全部当回调，才会出现误导性的"缺少 auth code"）。
func (s *LoginSession) handleNotCallback(w http.ResponseWriter, r *http.Request) {
	writeLoginPage(w, http.StatusNotFound, "这不是 Trae 登录页",
		"此地址只用于接收 Trae 授权回调，直接打开不会完成登录。<br>"+
			"请回到 Aether Cockpit 点击「打开登录页面」；手机端没有 Trae 客户端时，请改用「手动 Token」接入。")
}

// writeLoginPage 输出统一的中文说明页（带 charset，手机浏览器可读）。
func writeLoginPage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w,
		`<!doctype html><html><head><meta charset="utf-8">`+
			`<meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title></head>`+
			`<body style="font-family:sans-serif;padding:32px;line-height:1.6;max-width:640px;margin:0 auto">`+
			`<h2>%s</h2><p>%s</p></body></html>`, title, title, body)
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
