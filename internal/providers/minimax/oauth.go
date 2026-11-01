package minimax

// MiniMax Code 的 OAuth 设备登录（mcode 0.6.12 的 device flow）：
//   POST {account}/oauth2/device/code  → user_code + verification_uri
//   POST {account}/oauth2/token        → 轮询授权结果 / refresh_token
// PKCE S256，client_id mcode-public，scope agent.default，audience agent-backend。
//
// 这是官方客户端的登录方式；与手动粘贴 token 并存。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const (
	mcodeClientID   = "mcode-public"
	mcodeScope      = "agent.default"
	mcodeAudience   = "agent-backend"
	deviceGrant     = "urn:ietf:params:oauth:grant-type:device_code"
	accountBaseCN   = "https://account.minimax.cn"
	accountBaseIntl = "https://account.minimax.io"
)

// AccountBaseOverride 供自建代理/测试覆盖账号 OAuth 主机（AETHER_MINIMAX_ACCOUNT_BASE）。
var AccountBaseOverride = strings.TrimSpace(os.Getenv("AETHER_MINIMAX_ACCOUNT_BASE"))

func accountBase(site string) string {
	if AccountBaseOverride != "" {
		return strings.TrimRight(AccountBaseOverride, "/")
	}
	if site == intlProfile.id {
		return accountBaseIntl
	}
	return accountBaseCN
}

// DeviceFlow 是一次设备授权的客户端状态（verifier 不落盘、不外传）。
type DeviceFlow struct {
	Verifier   string
	DeviceCode string
	UserCode   string
	URL        string
	Interval   time.Duration
	Deadline   time.Time
	byUser     bool
}

// TokenSet 是设备授权/刷新得到的令牌。
type TokenSet struct {
	Access    string
	Refresh   string
	ExpiresAt int64 // unix 秒
	Subject   string
}

func pkce() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func postForm(ctx context.Context, client *http.Client, endpoint string, values url.Values) (map[string]any, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("MiniMax 登录服务返回的不是 JSON（HTTP %d）", resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func jsNum(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func jsStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// StartDevice 申请一次设备码。
func StartDevice(ctx context.Context, site string, client *http.Client) (*DeviceFlow, error) {
	if site != intlProfile.id {
		site = cnProfile.id
	}
	verifier, challenge, err := pkce()
	if err != nil {
		return nil, err
	}
	values := url.Values{
		"client_id":             {mcodeClientID},
		"scope":                 {mcodeScope},
		"audience":              {mcodeAudience},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	body, status, err := postForm(ctx, client, accountBase(site)+"/oauth2/device/code", values)
	if err != nil {
		return nil, err
	}
	if e := jsStr(body, "error"); e != "" || status < 200 || status >= 300 {
		return nil, fmt.Errorf("MiniMax 设备登录失败: %s", orDefault(e, fmt.Sprintf("HTTP %d", status)))
	}
	userCode := jsStr(body, "user_code")
	deviceCode := jsStr(body, "device_code")
	uri := jsStr(body, "verification_uri")
	if uri == "" {
		uri = jsStr(body, "verification_url")
	}
	if userCode == "" || uri == "" {
		return nil, fmt.Errorf("MiniMax 设备登录返回不完整")
	}
	byUser := deviceCode == "" && jsNum(body["expired_in"]) > 0
	expiredIn := jsNum(body["expired_in"])
	expiresIn := jsNum(body["expires_in"])
	if expiresIn == 0 && expiredIn > 0 {
		if expiredIn < 1e12 {
			expiresIn = expiredIn
		} else {
			expiresIn = (expiredIn - float64(time.Now().UnixMilli())) / 1000
		}
	}
	if expiresIn <= 0 {
		expiresIn = 600
	}
	interval := jsNum(body["interval"])
	if byUser && interval > 0 {
		// 这个变体的 interval 是毫秒。
		interval = interval / 1000
	}
	if interval <= 0 {
		interval = 5
	}
	complete := jsStr(body, "verification_uri_complete")
	if complete != "" {
		uri = complete
	}
	return &DeviceFlow{
		Verifier: verifier, DeviceCode: deviceCode, UserCode: userCode, URL: uri,
		Interval: time.Duration(interval) * time.Second,
		Deadline: time.Now().Add(time.Duration(expiresIn) * time.Second),
		byUser:   byUser,
	}, nil
}

// PollDevice 轮询一次。state: pending / done；done 时返回令牌。
func PollDevice(ctx context.Context, site string, f *DeviceFlow, client *http.Client) (*TokenSet, string, error) {
	if site != intlProfile.id {
		site = cnProfile.id
	}
	values := url.Values{
		"grant_type":    {deviceGrant},
		"client_id":     {mcodeClientID},
		"code_verifier": {f.Verifier},
	}
	if f.byUser {
		values.Set("user_code", f.UserCode)
	} else {
		values.Set("device_code", f.DeviceCode)
	}
	body, status, err := postForm(ctx, client, accountBase(site)+"/oauth2/token", values)
	if err != nil {
		return nil, "", err
	}
	e := jsStr(body, "error")
	st := jsStr(body, "status")
	switch {
	case e == "authorization_pending" || (status < 300 && st == "pending"):
		return nil, "pending", nil
	case e == "slow_down" || (status < 300 && st == "slow_down"):
		return nil, "slow_down", nil
	case e == "access_denied" || (status < 300 && (st == "denied" || st == "access_denied")):
		return nil, "", fmt.Errorf("MiniMax 登录被拒绝")
	case e == "expired_token" || (status < 300 && (st == "expired" || st == "expired_token")):
		return nil, "", fmt.Errorf("MiniMax 登录码已过期")
	case e != "" || status < 200 || status >= 300:
		return nil, "", fmt.Errorf("MiniMax 登录失败: %s", orDefault(e, fmt.Sprintf("HTTP %d", status)))
	}
	access := jsStr(body, "access_token")
	expiresIn := jsNum(body["expires_in"])
	if access == "" || expiresIn <= 0 {
		return nil, "", fmt.Errorf("MiniMax 登录返回的令牌不完整")
	}
	return &TokenSet{
		Access: access, Refresh: jsStr(body, "refresh_token"),
		ExpiresAt: time.Now().Add(time.Duration(expiresIn) * time.Second).Unix(),
	}, "done", nil
}

// RefreshTokens 用 refresh_token 换新令牌；baseOverride 非空时覆盖账号 OAuth 主机。
func RefreshTokens(ctx context.Context, site, refresh, baseOverride string, client *http.Client) (*TokenSet, error) {
	if site != intlProfile.id {
		site = cnProfile.id
	}
	if strings.TrimSpace(refresh) == "" {
		return nil, fmt.Errorf("MiniMax 缺少 refresh_token")
	}
	base := accountBase(site)
	if strings.TrimSpace(baseOverride) != "" {
		base = strings.TrimRight(strings.TrimSpace(baseOverride), "/")
	}
	body, status, err := postForm(ctx, client, base+"/oauth2/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {mcodeClientID},
	})
	if err != nil {
		return nil, err
	}
	access := jsStr(body, "access_token")
	expiresIn := jsNum(body["expires_in"])
	if e := jsStr(body, "error"); e != "" || access == "" || expiresIn <= 0 || status < 200 || status >= 300 {
		return nil, fmt.Errorf("MiniMax 令牌刷新失败: %s", orDefault(e, fmt.Sprintf("HTTP %d", status)))
	}
	return &TokenSet{
		Access: access, Refresh: orDefault(jsStr(body, "refresh_token"), refresh),
		ExpiresAt: time.Now().Add(time.Duration(expiresIn) * time.Second).Unix(),
	}, nil
}

// RefreshCredential 实现 TokenRefreshCapability：用 refresh_token 续期。
func (p *Provider) RefreshCredential(ctx context.Context, env *provider.AccountEnv) (*provider.CredentialDraft, error) {
	refresh := strings.TrimSpace(env.GetCredential("refresh_token"))
	if refresh == "" {
		return &provider.CredentialDraft{
			Label: env.Label,
			Credentials: map[string]string{
				"access_token": accessToken(env),
				"expires_at":   env.GetCredential("expires_at"),
			},
		}, nil
	}
	baseOverride := strings.TrimRight(strings.TrimSpace(env.GetCredential("account_base_url")), "/")
	ts, err := RefreshTokens(ctx, p.profile.id, refresh, baseOverride, env.Client())
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, err.Error()).WithProvider(p.profile.id)
	}
	exp := time.Unix(ts.ExpiresAt, 0).UTC().Format(time.RFC3339)
	env.SetCredential("access_token", ts.Access)
	env.SetCredential("refresh_token", ts.Refresh)
	env.SetCredential("expires_at", exp)
	return &provider.CredentialDraft{
		Label:     env.Label,
		ExpiresAt: ts.ExpiresAt,
		Credentials: map[string]string{
			"access_token":  ts.Access,
			"refresh_token": ts.Refresh,
			"expires_at":    exp,
			"user_id":       p.userID(env),
		},
	}, nil
}

// IdentityInfo 读取账号的 realUserID / 邮箱 / 名称（OAuth 登录后填账号标签）。
func (p *Provider) IdentityInfo(ctx context.Context, env *provider.AccountEnv) (uid, email, name string, err error) {
	token := strings.TrimSpace(accessToken(env))
	if token == "" {
		return "", "", "", fmt.Errorf("MiniMax 缺少 access token")
	}
	req, err := p.signedRequest(ctx, env, http.MethodGet, "/v1/api/user/info", token, "0", "")
	if err != nil {
		return "", "", "", err
	}
	resp, err := env.Client().Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var body struct {
		Data struct {
			UserInfo map[string]any `json:"userInfo"`
		} `json:"data"`
		UserInfo map[string]any `json:"userInfo"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return "", "", "", fmt.Errorf("MiniMax 用户信息不是 JSON")
	}
	u := body.Data.UserInfo
	if u == nil {
		u = body.UserInfo
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := u[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	return pick("realUserID", "real_user_id"), pick("userEmail", "email", "userMail", "user_email"), pick("userName", "name", "user_name"), nil
}
