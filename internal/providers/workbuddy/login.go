package workbuddy

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

	"aether-cockpit/internal/provider"
)

// WorkBuddy 登录：copilot.tencent.com 的 auth/state → 浏览器登录 → auth/token 轮询。
// 参考 cockpit-tools 的 workbuddy_oauth；不依赖官方客户端。

var wbAPIBase = "https://copilot.tencent.com"

const (
	wbLoginTimeout = 10 * time.Minute
	wbPollInterval = 2 * time.Second
)

// LoginSession 是一次进行中的 WorkBuddy 登录。
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

	state      string
	loginID    string
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
	wbMu       sync.Mutex
	wbSessions = map[string]*LoginSession{}
)

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// StartLogin 创建登录会话（providerID: workbuddy-cn / workbuddy-intl）。
func StartLogin(providerID string) (*LoginSession, error) {
	base, ok := provider.Get(providerID)
	if !ok {
		return nil, fmt.Errorf("渠道未注册: %s", providerID)
	}
	p, ok := base.(*Provider)
	if !ok || p.profile.family != "workbuddy" {
		return nil, fmt.Errorf("渠道 %s 不是 WorkBuddy", providerID)
	}

	state, authURL, err := requestLoginState()
	if err != nil {
		return nil, err
	}
	if authURL == "" {
		authURL = wbAPIBase + "/login?state=" + url.QueryEscape(state)
	}
	if u, err := url.Parse(authURL); err == nil {
		q := u.Query()
		q.Set("loginSessionId", randomHex(16))
		u.RawQuery = q.Encode()
		authURL = u.String()
	}

	s := &LoginSession{
		ID:         "workbuddy_" + randomHex(8),
		ProviderID: providerID,
		LoginURL:   authURL,
		State:      "pending",
		Interval:   int(wbPollInterval / time.Second),
		CreatedAt:  time.Now().Unix(),
		ExpiresAt:  time.Now().Add(wbLoginTimeout).Unix(),
		state:      state,
	}
	wbMu.Lock()
	wbSessions[s.ID] = s
	wbMu.Unlock()
	go pollLogin(s)
	return s, nil
}

func requestLoginState() (string, string, error) {
	body, err := httpJSON("POST", wbAPIBase+"/v2/plugin/auth/state?platform=workbuddy", map[string]any{}, false)
	if err != nil {
		return "", "", fmt.Errorf("请求 WorkBuddy auth/state 失败: %w", err)
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
	wbMu.Lock()
	defer wbMu.Unlock()
	s := wbSessions[id]
	if s == nil {
		return nil
	}
	cp := *s
	return &cp
}

// CancelLogin 取消会话。
func CancelLogin(id string) {
	wbMu.Lock()
	if s := wbSessions[id]; s != nil && s.State == "pending" {
		s.State = "failed"
		s.Err = "用户取消"
	}
	wbMu.Unlock()
}

// MarkCommitted 标记账号已创建。
func MarkCommitted(id, accountID string) bool {
	wbMu.Lock()
	defer wbMu.Unlock()
	s := wbSessions[id]
	if s == nil || s.State != "done" || s.committed {
		return false
	}
	s.committed = true
	s.Account = accountID
	return true
}

// Credentials 返回完成后的凭证。
func Credentials(id string) map[string]string {
	wbMu.Lock()
	defer wbMu.Unlock()
	s := wbSessions[id]
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
	}
	if s.refresh != "" {
		creds["refresh_token"] = s.refresh
	}
	return creds
}

func pollLogin(s *LoginSession) {
	ticker := time.NewTicker(wbPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		wbMu.Lock()
		if s.State != "pending" {
			wbMu.Unlock()
			return
		}
		if time.Now().Unix() > s.ExpiresAt {
			s.State = "expired"
			s.Err = "登录超时"
			wbMu.Unlock()
			return
		}
		state := s.state
		wbMu.Unlock()

		body, err := httpJSON("GET", wbAPIBase+"/v2/plugin/auth/token?state="+url.QueryEscape(state), nil, false)
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
		uid, nickname, email, enterprise := fetchAccount(state, token, domain)

		wbMu.Lock()
		if s.State != "pending" {
			wbMu.Unlock()
			return
		}
		s.token, s.refresh, s.domain = token, refresh, domain
		s.userID, s.userName, s.userEmail, s.enterprise = uid, nickname, email, enterprise
		s.State = "done"
		wbMu.Unlock()
		return
	}
}

func fetchAccount(state, token, domain string) (uid, nickname, email, enterprise string) {
	req, err := http.NewRequest(http.MethodGet, wbAPIBase+"/v2/plugin/login/account?state="+url.QueryEscape(state), nil)
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
