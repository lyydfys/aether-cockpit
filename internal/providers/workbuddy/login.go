package workbuddy

import (
	"bytes"
	"context"
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

// WorkBuddy 登录：{站点根}/v2/plugin/auth/state → 浏览器登录 → auth/token 轮询。
// 参考 local-vault 的 workbuddy_oauth；不依赖官方客户端。
//
// 国内版与国际版的接口路径完全相同，只有站点根不同：
//
//	workbuddy-cn   → https://copilot.tencent.com
//	workbuddy-intl → https://www.workbuddy.ai
//
// 站点根必须按渠道推导（见 loginAPIBase）：authUrl 由服务端按站点签发，
// 拿国内站点的 state 去换，返回的登录页也只能是国内站点的。

// wbAPIBaseOverride 是测试钩子：非空时替换登录链路的站点根。
var wbAPIBaseOverride = ""

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

	// base 是本次登录所属的站点根（国际版 / 国内版不同），
	// 轮询与取账号都必须回到同一个站点，不能再用包级常量。
	base       string
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

// loginAPIBase 返回某渠道登录链路所在的站点根。
//
// 登录接口（auth/state → auth/token → login/account）挂在站点根上，路径固定为
// /v2/plugin/...；而 profile.baseURL 是对话端点（{站点根}/v2），所以要先把版本段
// 去掉。国内版与国际版只有这个根不同，写死任意一个都会串区。
func loginAPIBase(p *Provider) string {
	if wbAPIBaseOverride != "" {
		return wbAPIBaseOverride
	}
	return providerOrigin(p.profile.baseURL)
}

// providerOrigin 去掉 baseURL 末尾的版本段（/v2），得到站点根。
// 例：https://www.workbuddy.ai/v2 → https://www.workbuddy.ai。
func providerOrigin(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return strings.TrimSuffix(base, "/v2")
}

// StartLogin 创建登录会话（providerID: workbuddy-cn / workbuddy-intl）。
func StartLogin(providerID string) (*LoginSession, error) {
	prov, ok := provider.Get(providerID)
	if !ok {
		return nil, fmt.Errorf("渠道未注册: %s", providerID)
	}
	p, ok := prov.(*Provider)
	if !ok || p.profile.family != "workbuddy" {
		return nil, fmt.Errorf("渠道 %s 不是 WorkBuddy", providerID)
	}

	// 关键：登录站点跟着渠道走（国际版走 www.workbuddy.ai），不能固定国内域名。
	apiBase := loginAPIBase(p)
	state, authURL, err := requestLoginState(apiBase, p.profile.intl)
	if err != nil {
		return nil, err
	}
	if authURL == "" {
		authURL = apiBase + "/login?state=" + url.QueryEscape(state)
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
		base:       apiBase,
		state:      state,
	}
	wbMu.Lock()
	wbSessions[s.ID] = s
	wbMu.Unlock()
	go pollLogin(s)
	return s, nil
}

func requestLoginState(base string, intl bool) (string, string, error) {
	body, err := httpJSON("POST", base+"/v2/plugin/auth/state?platform=workbuddy", map[string]any{}, false, intl)
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
		base := s.base
		intl := isIntlProvider(s.ProviderID)
		wbMu.Unlock()

		body, err := httpJSON("GET", base+"/v2/plugin/auth/token?state="+url.QueryEscape(state), nil, false, intl)
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
		uid, nickname, email, enterprise := fetchAccount(base, state, token, domain, intl)

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

// isIntlProvider 判断登录会话属于国际版（决定 Accept-Language / 默认域名）。
func isIntlProvider(providerID string) bool {
	return providerID == intlProfile.id
}

func fetchAccount(base, state, token, domain string, intl bool) (uid, nickname, email, enterprise string) {
	req, err := http.NewRequest(http.MethodGet, base+"/v2/plugin/login/account?state="+url.QueryEscape(state), nil)
	if err != nil {
		return
	}
	lang := "zh-CN"
	if intl {
		lang = "en-US"
	}
	origin := regionOrigin(intl)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", lang)
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	setPluginHeaders(req.Header, currentIdentity())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-No-User-Id", "true")
	req.Header.Set("X-No-Enterprise-Id", "true")
	req.Header.Set("X-No-Department-Info", "true")
	if domain == "" {
		domain = regionDefaultDomain(intl)
	}
	req.Header.Set("X-Domain", domain)
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

// httpJSON 发一次登录链路请求。
//
// endpoint 不能用名为 url 的参数：会遮蔽 net/url 包（历史坑）。
func httpJSON(method, endpoint string, payload any, authorized bool, intl bool) (map[string]any, error) {
	var body io.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	lang := "zh-CN"
	if intl {
		lang = "en-US"
	}
	origin := regionOrigin(intl)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// 对齐参考实现 LoginApi.noAuth：UA（按身份档位）+ 插件头 +
	// Accept-Language / Origin / Referer / X-Domain（区域默认域名）。
	req.Header.Set("Accept-Language", lang)
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	setPluginHeaders(req.Header, currentIdentity())
	req.Header.Set("X-Domain", regionDefaultDomain(intl))
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
