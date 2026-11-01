package codebuddy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// CodeBuddy 登录：{base}/v2/plugin/auth/state → 浏览器登录 → auth/token 轮询。
// 参考 cockpit-tools codebuddy_oauth；国际/国内仅 baseURL 不同。

var cbAPIBaseOverride = ""

const (
	cbLoginTimeout = 10 * time.Minute
	cbPollInterval = 2 * time.Second
)

// LoginSession 是一次进行中的 CodeBuddy 登录。
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

	base       string
	state      string
	token      string
	refresh    string
	userID     string
	userName   string
	userEmail  string
	domain     string
	enterprise string

	committed bool
}

var (
	cbMu       sync.Mutex
	cbSessions = map[string]*LoginSession{}
)

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func apiBase(p *Provider) string {
	if cbAPIBaseOverride != "" {
		return cbAPIBaseOverride
	}
	return p.profile.baseURL
}

// StartLogin 创建登录会话（providerID: codebuddy-intl / codebuddy-cn）。
func StartLogin(providerID string) (*LoginSession, error) {
	p, err := Validate(providerID)
	if err != nil {
		return nil, err
	}
	base := apiBase(p)
	state, authURL, err := requestLoginState(base)
	if err != nil {
		return nil, err
	}
	if authURL == "" {
		authURL = base + "/login?state=" + url.QueryEscape(state)
	}
	if u, err := url.Parse(authURL); err == nil {
		q := u.Query()
		q.Set("loginSessionId", randomHex(16))
		u.RawQuery = q.Encode()
		authURL = u.String()
	}
	s := &LoginSession{
		ID:         "codebuddy_" + randomHex(8),
		ProviderID: providerID,
		LoginURL:   authURL,
		State:      "pending",
		Interval:   int(cbPollInterval / time.Second),
		CreatedAt:  time.Now().Unix(),
		ExpiresAt:  time.Now().Add(cbLoginTimeout).Unix(),
		base:       base,
		state:      state,
	}
	cbMu.Lock()
	cbSessions[s.ID] = s
	cbMu.Unlock()
	go pollLogin(s)
	return s, nil
}

func requestLoginState(base string) (string, string, error) {
	body, err := httpJSON("POST", base+"/v2/plugin/auth/state?platform=ide", map[string]any{}, false)
	if err != nil {
		return "", "", fmt.Errorf("请求 CodeBuddy auth/state 失败: %w", err)
	}
	data, _ := body["data"].(map[string]any)
	state, _ := data["state"].(string)
	authURL, _ := data["authUrl"].(string)
	if authURL == "" {
		authURL, _ = data["auth_url"].(string)
	}
	if strings.TrimSpace(state) == "" {
		return "", "", fmt.Errorf("auth/state 响应缺少 state")
	}
	return strings.TrimSpace(state), strings.TrimSpace(authURL), nil
}

// GetLogin 返回会话副本。
func GetLogin(id string) *LoginSession {
	cbMu.Lock()
	defer cbMu.Unlock()
	s := cbSessions[id]
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

// CancelLogin 取消会话。
func CancelLogin(id string) {
	cbMu.Lock()
	if s := cbSessions[id]; s != nil && s.State == "pending" {
		s.State = "failed"
		s.Err = "用户取消"
	}
	cbMu.Unlock()
}

// MarkCommitted 标记账号已创建。
func MarkCommitted(id, accountID string) bool {
	cbMu.Lock()
	defer cbMu.Unlock()
	s := cbSessions[id]
	if s == nil || s.State != "done" || s.committed {
		return false
	}
	s.committed = true
	s.Account = accountID
	return true
}

// Credentials 返回完成后的凭证。
func Credentials(id string) map[string]string {
	cbMu.Lock()
	defer cbMu.Unlock()
	s := cbSessions[id]
	if s == nil || s.State != "done" || s.token == "" {
		return nil
	}
	creds := map[string]string{
		"token":         s.token,
		"user_id":       s.userID,
		"user_name":     s.userName,
		"user_email":    s.userEmail,
		"domain":        s.domain,
		"enterprise_id": s.enterprise,
		"base_url":      s.base,
	}
	if s.refresh != "" {
		creds["refresh_token"] = s.refresh
	}
	return creds
}

func pollLogin(s *LoginSession) {
	ticker := time.NewTicker(cbPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		cbMu.Lock()
		if s.State != "pending" {
			cbMu.Unlock()
			return
		}
		if time.Now().Unix() > s.ExpiresAt {
			s.State = "expired"
			s.Err = "登录超时"
			cbMu.Unlock()
			return
		}
		state := s.state
		base := s.base
		cbMu.Unlock()

		body, err := httpJSON("GET", base+"/v2/plugin/auth/token?state="+url.QueryEscape(state), nil, false)
		if err != nil {
			continue
		}
		code := int64(-1)
		switch v := body["code"].(type) {
		case float64:
			code = int64(v)
		case int64:
			code = v
		}
		if code != 0 && code != 200 {
			continue
		}
		data, _ := body["data"].(map[string]any)
		token := firstString(data, "accessToken", "access_token")
		if token == "" {
			continue
		}
		refresh := firstString(data, "refreshToken", "refresh_token")
		domain := firstString(data, "domain")
		uid, nickname, email, enterprise := fetchAccount(base, state, token, domain)

		cbMu.Lock()
		if s.State != "pending" {
			cbMu.Unlock()
			return
		}
		s.token, s.refresh, s.domain = token, refresh, domain
		s.userID, s.userName, s.userEmail, s.enterprise = uid, nickname, email, enterprise
		s.State = "done"
		cbMu.Unlock()
		return
	}
}

func fetchAccount(base, state, token, domain string) (uid, nickname, email, enterprise string) {
	req, err := http.NewRequest(http.MethodGet, base+"/v2/plugin/login/account?state="+url.QueryEscape(state), nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-No-User-Id", "true")
	req.Header.Set("X-No-Enterprise-Id", "true")
	req.Header.Set("X-No-Department-Info", "true")
	if domain != "" {
		req.Header.Set("X-Domain", domain)
	}
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return
	}
	data, _ := body["data"].(map[string]any)
	uid = firstString(data, "uid")
	nickname = firstString(data, "nickname")
	email = firstString(data, "email")
	enterprise = firstString(data, "enterpriseId")
	if email == "" {
		email = nickname
	}
	if email == "" {
		email = uid
	}
	return
}

func httpJSON(method, url string, payload any, authorized bool) (map[string]any, error) {
	var body io.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if !authorized {
		req.Header.Set("X-No-Authorization", "true")
		req.Header.Set("X-No-User-Id", "true")
		req.Header.Set("X-No-Enterprise-Id", "true")
		req.Header.Set("X-No-Department-Info", "true")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
