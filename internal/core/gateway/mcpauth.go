package gateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MCP 远程服务器的 OAuth 协议核心（aether mcp-signin 的对等实现）：
// 受保护资源元数据 → 授权服务器元数据 → 动态客户端注册 → PKCE 授权码 →
// 令牌交换与刷新。只做协议与 HTTP，不负责浏览器与存储（见 mcp_signin.go）。

type oauthASMeta struct {
	Issuer                 string   `json:"issuer"`
	AuthorizationEndpoint  string   `json:"authorization_endpoint"`
	TokenEndpoint          string   `json:"token_endpoint"`
	RevocationEndpoint     string   `json:"revocation_endpoint"`
	RegistrationEndpoint   string   `json:"registration_endpoint"`
	ScopesSupported        []string `json:"scopes_supported"`
	CodeChallengeMethods   []string `json:"code_challenge_methods_supported"`
	GrantTypesSupported    []string `json:"grant_types_supported"`
	TokenAuthMethods       []string `json:"token_endpoint_auth_methods_supported"`
	ResourceIndicators     bool     `json:"-"`
	ProtectedResourceMeta  string   `json:"-"`
	ProtectedResourceScope []string `json:"-"`
}

type oauthClient struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	RegisteredAt int64  `json:"registered_at,omitempty"`
}

type oauthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
}

func (t oauthToken) expired(now time.Time) bool {
	if t.AccessToken == "" {
		return true
	}
	if t.ExpiresAt == 0 {
		return false
	}
	return now.Unix() >= t.ExpiresAt
}

func oauthHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}

// errOAuthRejected 表示授权服务器明确拒绝（invalid_grant 等 400/401），
// 与网络错误区分：只有被拒绝才把登录标记为失效。
var errOAuthRejected = errors.New("授权服务器拒绝了令牌请求")

func randB64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// newPKCE 生成 S256 的 code_verifier / code_challenge。
func newPKCE() (verifier, challenge string, err error) {
	verifier, err = randB64(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// oauthResourceMeta 是 RFC 9728 的受保护资源元数据（只取用到的字段）。
type oauthResourceMeta struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
	BearerMethods        []string `json:"bearer_methods_supported"`
}

func readJSONBody(resp *http.Response, out any) error {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, clipText(string(raw), 200))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("响应不是 JSON: %w", err)
	}
	return nil
}

// probeMCPAuth 向服务器发一次 initialize，从 401 里取 resource_metadata 地址
// 与 scope；服务器不需要授权时返回明确错误（而不是假装要登录）。
func probeMCPAuth(ctx context.Context, serverURL string, hc *http.Client) (metaURL, scope string, err error) {
	body := strings.NewReader(mcpInitBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, body)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("连接服务器失败: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return "", "", fmt.Errorf("该服务器不需要登录（已可直接访问）")
		}
		return "", "", fmt.Errorf("服务器返回 %s，未提供登录信息", resp.Status)
	}
	for _, h := range resp.Header.Values("WWW-Authenticate") {
		params := parseBearerParams(h)
		if params["resource_metadata"] != "" {
			return params["resource_metadata"], params["scope"], nil
		}
		if scope == "" {
			scope = params["scope"]
		}
	}
	return "", scope, nil
}

// parseBearerParams 解析 `Bearer a="b", c=d` 形式（值可带引号、可含逗号）。
func parseBearerParams(header string) map[string]string {
	out := map[string]string{}
	h := strings.TrimSpace(header)
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		h = strings.TrimSpace(h[len("bearer "):])
	}
	for len(h) > 0 {
		eq := strings.IndexByte(h, '=')
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(h[:eq])
		h = strings.TrimSpace(h[eq+1:])
		var val string
		if strings.HasPrefix(h, `"`) {
			var b strings.Builder
			i := 1
			for i < len(h) && h[i] != '"' {
				if h[i] == '\\' && i+1 < len(h) {
					i++
				}
				b.WriteByte(h[i])
				i++
			}
			val = b.String()
			if i < len(h) {
				i++
			}
			h = h[i:]
			if c := strings.IndexByte(h, ','); c >= 0 {
				h = h[c+1:]
			} else {
				h = ""
			}
		} else {
			c := strings.IndexByte(h, ',')
			if c >= 0 {
				val, h = strings.TrimSpace(h[:c]), h[c+1:]
			} else {
				val, h = strings.TrimSpace(h), ""
			}
		}
		if key != "" {
			out[strings.ToLower(key)] = val
		}
		h = strings.TrimSpace(h)
	}
	return out
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func pathOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(u.Path, "/")
}

// discoverResource 找受保护资源元数据：优先 401 给出的地址，
// 否则按 RFC 9728 试 origin 与带路径的 well-known。
func discoverResource(ctx context.Context, serverURL, metaURL string, hc *http.Client) (oauthResourceMeta, error) {
	candidates := []string{}
	if metaURL != "" {
		candidates = append(candidates, metaURL)
	}
	origin := originOf(serverURL)
	if origin != "" {
		candidates = append(candidates, origin+"/.well-known/oauth-protected-resource")
		if p := pathOf(serverURL); p != "" && p != "/" {
			candidates = append(candidates, origin+"/.well-known/oauth-protected-resource"+p)
		}
	}
	var lastErr error
	for _, c := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		var meta oauthResourceMeta
		err = readJSONBody(resp, &meta)
		if err == nil && len(meta.AuthorizationServers) > 0 {
			return meta, nil
		}
		if err == nil {
			err = fmt.Errorf("%s 未列出 authorization_servers", c)
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("找不到受保护资源元数据")
	}
	return oauthResourceMeta{}, lastErr
}

// discoverAS 读授权服务器元数据（RFC 8414，回退 OIDC discovery）。
func discoverAS(ctx context.Context, issuer string, hc *http.Client) (oauthASMeta, error) {
	base := strings.TrimSuffix(issuer, "/")
	candidates := []string{
		base + "/.well-known/oauth-authorization-server",
		base + "/.well-known/openid-configuration",
	}
	var lastErr error
	for _, c := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Accept", "application/json")
		resp, err := hc.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		var meta oauthASMeta
		if err := readJSONBody(resp, &meta); err != nil {
			lastErr = err
			continue
		}
		if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" {
			lastErr = fmt.Errorf("%s 缺少 authorization_endpoint 或 token_endpoint", c)
			continue
		}
		if len(meta.CodeChallengeMethods) > 0 {
			ok := false
			for _, m := range meta.CodeChallengeMethods {
				if strings.EqualFold(m, "S256") {
					ok = true
					break
				}
			}
			if !ok {
				lastErr = fmt.Errorf("授权服务器不支持 PKCE S256")
				continue
			}
		}
		return meta, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("找不到授权服务器元数据")
	}
	return oauthASMeta{}, lastErr
}

// registerOAuthClient 动态注册公开客户端（RFC 7591）。
func registerOAuthClient(ctx context.Context, meta oauthASMeta, redirectURI, scope string, hc *http.Client) (oauthClient, error) {
	if meta.RegistrationEndpoint == "" {
		return oauthClient{}, fmt.Errorf("授权服务器不支持动态客户端注册，且未提供 client_id")
	}
	payload := map[string]any{
		"client_name":                "Aether Cockpit",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
		"application_type":           "native",
	}
	if scope != "" {
		payload["scope"] = scope
	}
	raw, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RegistrationEndpoint, strings.NewReader(string(raw)))
	if err != nil {
		return oauthClient{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return oauthClient{}, fmt.Errorf("注册客户端失败: %w", err)
	}
	var out oauthClient
	if err := readJSONBody(resp, &out); err != nil {
		return oauthClient{}, fmt.Errorf("注册客户端失败: %w", err)
	}
	if out.ClientID == "" {
		return oauthClient{}, fmt.Errorf("注册客户端失败: 响应缺少 client_id")
	}
	out.RedirectURI = redirectURI
	out.RegisteredAt = time.Now().Unix()
	return out, nil
}

// revokeOAuthToken 尽力撤销服务商侧的授权（RFC 7009）；失败不影响本地退出。
func revokeOAuthToken(ctx context.Context, meta oauthASMeta, client oauthClient, token oauthToken, hc *http.Client) error {
	if meta.RevocationEndpoint == "" {
		return nil
	}
	tok, hint := token.RefreshToken, "refresh_token"
	if tok == "" {
		tok, hint = token.AccessToken, "access_token"
	}
	if tok == "" {
		return nil
	}
	form := url.Values{"token": {tok}, "token_type_hint": {hint}, "client_id": {client.ClientID}}
	if client.ClientSecret != "" {
		form.Set("client_secret", client.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RevocationEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("撤销令牌返回 %s", resp.Status)
}

func tokenExpiry(resp map[string]any) int64 {
	if v, ok := resp["expires_in"].(float64); ok && v > 0 {
		return time.Now().Add(time.Duration(v)*time.Second - 30*time.Second).Unix()
	}
	return 0
}

// exchangeOAuthCode 用授权码换令牌（client_secret_post 或公开客户端）。
func exchangeOAuthCode(ctx context.Context, meta oauthASMeta, client oauthClient, code, verifier, redirectURI, resource string, hc *http.Client) (oauthToken, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {client.ClientID},
		"code_verifier": {verifier},
	}
	if client.ClientSecret != "" {
		form.Set("client_secret", client.ClientSecret)
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return oauthToken{}, fmt.Errorf("令牌请求失败: %w", err)
	}
	var body map[string]any
	if err := readJSONBody(resp, &body); err != nil {
		return oauthToken{}, fmt.Errorf("令牌请求失败: %w", err)
	}
	out := oauthToken{
		AccessToken:  stringOf(body["access_token"]),
		RefreshToken: stringOf(body["refresh_token"]),
		TokenType:    stringOf(body["token_type"]),
		Scope:        stringOf(body["scope"]),
		ExpiresAt:    tokenExpiry(body),
	}
	if out.AccessToken == "" {
		return oauthToken{}, fmt.Errorf("令牌响应缺少 access_token")
	}
	return out, nil
}

// refreshOAuthToken 用 refresh_token 续期；失败时返回错误，由调用方标记失效。
func refreshOAuthToken(ctx context.Context, meta oauthASMeta, client oauthClient, token oauthToken, resource string, hc *http.Client) (oauthToken, error) {
	if token.RefreshToken == "" {
		return oauthToken{}, fmt.Errorf("没有 refresh_token，无法续期")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {token.RefreshToken},
		"client_id":     {client.ClientID},
	}
	if client.ClientSecret != "" {
		form.Set("client_secret", client.ClientSecret)
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return oauthToken{}, fmt.Errorf("续期失败: %w", err)
	}
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		return oauthToken{}, fmt.Errorf("%w: %s", errOAuthRejected, clipText(string(raw), 200))
	}
	var body map[string]any
	if err := readJSONBody(resp, &body); err != nil {
		return oauthToken{}, fmt.Errorf("续期失败: %w", err)
	}
	out := oauthToken{
		AccessToken:  stringOf(body["access_token"]),
		RefreshToken: stringOf(body["refresh_token"]),
		TokenType:    stringOf(body["token_type"]),
		Scope:        stringOf(body["scope"]),
		ExpiresAt:    tokenExpiry(body),
	}
	if out.AccessToken == "" {
		return oauthToken{}, fmt.Errorf("续期响应缺少 access_token")
	}
	if out.RefreshToken == "" {
		out.RefreshToken = token.RefreshToken
	}
	return out, nil
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

// oauthAuthorizeURL 拼授权地址（RFC 6749 + PKCE + RFC 8707 resource）。
func oauthAuthorizeURL(meta oauthASMeta, clientID, redirectURI, state, challenge, scope, resource string) string {
	u, err := url.Parse(meta.AuthorizationEndpoint)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if scope != "" {
		q.Set("scope", scope)
	}
	if resource != "" {
		q.Set("resource", resource)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
