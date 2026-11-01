package claude

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
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// sessionKey 引导授权：把浏览器 claude.ai 的 sessionKey Cookie 换成
// 标准 OAuth 凭证（access_token / refresh_token），对标 relay-panel 的
// CookieAuth 三步流（repository/claude_oauth_service.go + service/oauth_service.go）。
//
// 适用场景：本机没有 Claude Code 的 credentials.json（例如 Claude Code
// 未登录或在别的机器上），只在浏览器里登录过 claude.ai 网页版时，
// 从浏览器 DevTools 复制 sessionKey Cookie 即可完成接入，无需手动抓 token。
//
// 三步流（请求形状逐项核对过 relay-panel 源码）：
//  1. GET https://claude.ai/api/organizations（Cookie: sessionKey=...）
//     → 组织数组 [{uuid, name, raven_type}]；多个组织时优先 raven_type=="team"，
//     否则取第一个；响应字段 raven_type 为指针，nil 表示个人组织；
//  2. POST https://claude.ai/v1/oauth/{orgUUID}/authorize（同样带 sessionKey
//     Cookie，Origin=https://claude.ai、Referer=https://claude.ai/new 伪装网页端，
//     JSON 体带 PKCE code_challenge + state）→ 响应 {redirect_uri}，
//     code 与 state 从其 query 解析（state 非空时以 "code#state" 形态传递）；
//  3. POST https://platform.claude.com/v1/oauth/token
//     （grant_type=authorization_code + code + code_verifier + client_id
//     + redirect_uri + state）→ {access_token, refresh_token, expires_in}。

// 端点做成包级 var：测试用 httptest 覆盖后即可离线验证全流程。
var (
	// claudeOrgsURL 第 1 步：列出账号所属组织。
	claudeOrgsURL = "https://claude.ai/api/organizations"
	// claudeAuthorizeURL 第 2 步的基地址，实际端点为
	// {claudeAuthorizeURL}/v1/oauth/{orgUUID}/authorize。
	claudeAuthorizeURL = "https://claude.ai"
	// claudeOAuthTokenURL 第 3 步：授权码换 token（relay-panel 的 oauth.TokenURL，
	// 注意与 auth.go 的刷新端点 console.anthropic.com 是不同域，两者均可用）。
	claudeOAuthTokenURL = "https://platform.claude.com/v1/oauth/token"
)

const (
	// sessionKeyScope 是 sessionKey 引导授权申请的 scope（relay-panel 的
	// oauth.ScopeAPI）：内部 API 调用不支持 org:create_api_key，因此不申请。
	sessionKeyScope = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"

	// claudeRedirectURI 是 OAuth 回调地址（relay-panel 的 oauth.RedirectURI），
	// 授权端点只回显在响应的 redirect_uri 里，不做真实跳转。
	claudeRedirectURI = "https://platform.claude.com/oauth/code/callback"

	// browserUA 用于第 1/2 步访问 claude.ai 网页后端：配合账号 env.HTTPClient
	// 的 uTLS Chrome 指纹（relay-panel 用 ImpersonateChrome 达到同样效果）。
	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// exchangeSessionKey 用浏览器 sessionKey Cookie 走三步流换取标准 OAuth 凭证。
//
// client 传账号的 env.HTTPClient（已绑定 uTLS Chrome 指纹与代理出口）；
// 返回的 draft 只含 access_token / refresh_token / expires_at 三个键，
// 由调用方写回账号（网关 applyCredential 按 key 合并，不会覆盖其它字段）。
func exchangeSessionKey(ctx context.Context, client *http.Client, sessionKey string) (*provider.CredentialDraft, error) {
	if client == nil {
		client = http.DefaultClient
	}
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "生成 PKCE 参数失败").WithCause(err)
	}
	state, err := randomB64URL(32)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "生成 OAuth state 失败").WithCause(err)
	}

	orgUUID, err := fetchOrganizationUUID(ctx, client, sessionKey)
	if err != nil {
		return nil, err
	}
	fullCode, err := fetchAuthorizationCode(ctx, client, sessionKey, orgUUID, challenge, state)
	if err != nil {
		return nil, err
	}
	token, err := exchangeCodeForToken(ctx, client, fullCode, verifier)
	if err != nil {
		return nil, err
	}

	draft := &provider.CredentialDraft{
		Label: "Claude 订阅 (sessionKey)",
		Credentials: map[string]string{
			"access_token":  token.AccessToken,
			"refresh_token": token.RefreshToken,
		},
	}
	if token.ExpiresIn > 0 {
		expiresAt := time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
		draft.Credentials["expires_at"] = expiresAt.Format(time.RFC3339)
		draft.ExpiresAt = expiresAt.Unix()
	}
	return draft, nil
}

// oauthToken 是第 3 步的响应结构。
type oauthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"` // 秒；订阅 token 常见 8 小时
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

// fetchOrganizationUUID 第 1 步：拿组织 UUID。
// 多个组织时优先 raven_type=="team"（团队订阅），否则取第一个。
func fetchOrganizationUUID(ctx context.Context, client *http.Client, sessionKey string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, claudeOrgsURL, nil)
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeInternal, "构造组织查询请求失败").WithCause(err)
	}
	applySessionKeyCookie(req, sessionKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 组织查询请求失败").
			WithCause(err).WithProvider(channelID).WithRetryable(true)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", sessionKeyStepError("获取组织", resp.StatusCode, body)
	}

	var orgs []struct {
		UUID      string  `json:"uuid"`
		Name      string  `json:"name"`
		RavenType *string `json:"raven_type"` // nil 表示个人组织，"team" 表示团队
	}
	if err := json.Unmarshal(body, &orgs); err != nil {
		return "", ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"Claude 组织响应无法解析：%s", truncate(string(body), 200)).WithCause(err).WithProvider(channelID)
	}
	if len(orgs) == 0 {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"sessionKey 对应的账号没有任何组织；请确认浏览器里登录的是 claude.ai 订阅账号").
			WithProvider(channelID)
	}
	uuid := orgs[0].UUID
	for _, org := range orgs[1:] {
		if org.RavenType != nil && *org.RavenType == "team" {
			uuid = org.UUID
			break
		}
	}
	if strings.TrimSpace(uuid) == "" {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"Claude 组织响应缺少 uuid 字段（可能字段结构已变化）").WithProvider(channelID)
	}
	return uuid, nil
}

// fetchAuthorizationCode 第 2 步：用 sessionKey + PKCE 拿授权码。
// 响应的 redirect_uri 形如 {claudeRedirectURI}?code=...&state=...；
// state 非空时按 relay-panel 形态返回 "code#state"，第 3 步再拆开原样回传。
func fetchAuthorizationCode(ctx context.Context, client *http.Client, sessionKey, orgUUID, challenge, state string) (string, error) {
	endpoint := strings.TrimRight(claudeAuthorizeURL, "/") + "/v1/oauth/" + url.PathEscape(orgUUID) + "/authorize"
	payload, err := json.Marshal(map[string]any{
		"response_type":         "code",
		"client_id":             defaultClientID,
		"organization_uuid":     orgUUID,
		"redirect_uri":          claudeRedirectURI,
		"scope":                 sessionKeyScope,
		"state":                 state,
		"code_challenge":        challenge,
		"code_challenge_method": "S256",
	})
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeInternal, "构造授权请求失败").WithCause(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeInternal, "构造授权请求失败").WithCause(err)
	}
	applySessionKeyCookie(req, sessionKey)
	// 伪装成 claude.ai 网页端发起的授权（relay-panel 同款头组）。
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Origin", "https://claude.ai")
	req.Header.Set("Referer", "https://claude.ai/new")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 授权请求失败").
			WithCause(err).WithProvider(channelID).WithRetryable(true)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", sessionKeyStepError("获取授权码", resp.StatusCode, body)
	}

	var authorize struct {
		RedirectURI string `json:"redirect_uri"`
	}
	if err := json.Unmarshal(body, &authorize); err != nil {
		return "", ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"Claude 授权响应无法解析：%s", truncate(string(body), 200)).WithCause(err).WithProvider(channelID)
	}
	if strings.TrimSpace(authorize.RedirectURI) == "" {
		return "", ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"Claude 授权响应缺少 redirect_uri：%s", truncate(string(body), 200)).WithProvider(channelID)
	}
	u, err := url.Parse(authorize.RedirectURI)
	if err != nil {
		return "", ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"Claude 授权响应的 redirect_uri 无法解析：%s", truncate(authorize.RedirectURI, 200)).
			WithCause(err).WithProvider(channelID)
	}
	q := u.Query()
	authCode := q.Get("code")
	if strings.TrimSpace(authCode) == "" {
		return "", ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"Claude 授权响应的 redirect_uri 不含授权码：%s", truncate(authorize.RedirectURI, 200)).
			WithProvider(channelID)
	}
	if respState := q.Get("state"); respState != "" {
		return authCode + "#" + respState, nil
	}
	return authCode, nil
}

// exchangeCodeForToken 第 3 步：授权码 + code_verifier 换 token。
// 请求形状照抄 relay-panel：JSON 体，Accept 与 UA 沿用其 resty 默认值。
func exchangeCodeForToken(ctx context.Context, client *http.Client, fullCode, verifier string) (*oauthToken, error) {
	authCode := fullCode
	reqBody := map[string]any{
		"grant_type":    "authorization_code",
		"code":          authCode,
		"client_id":     defaultClientID,
		"redirect_uri":  claudeRedirectURI,
		"code_verifier": verifier,
	}
	if idx := strings.Index(authCode, "#"); idx >= 0 {
		// 第 2 步返回的 "code#state" 组合形态：拆开后 state 原样回传。
		reqBody["code"] = authCode[:idx]
		reqBody["state"] = authCode[idx+1:]
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造令牌交换请求失败").WithCause(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeOAuthTokenURL, strings.NewReader(string(payload)))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造令牌交换请求失败").WithCause(err)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "axios/1.13.6")

	resp, err := client.Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 令牌交换请求失败").
			WithCause(err).WithProvider(channelID).WithRetryable(true)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth,
			"Claude 令牌交换失败（%d）：%s", resp.StatusCode, truncate(string(body), 200)).
			WithProvider(channelID)
	}

	var token oauthToken
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"Claude 令牌响应无法解析：%s", truncate(string(body), 200)).WithCause(err).WithProvider(channelID)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth,
			"Claude 令牌响应缺少 access_token：%s", truncate(string(body), 200)).WithProvider(channelID)
	}
	return &token, nil
}

// applySessionKeyCookie 给 claude.ai 的请求带上 sessionKey Cookie。
// 手动设置 Cookie 头而不是用 CookieJar：jar 会把第 2 步响应里的
// 其它 Cookie 混进后续请求，不利于行为确定性。
func applySessionKeyCookie(req *http.Request, sessionKey string) {
	req.Header.Set("Cookie", "sessionKey="+sessionKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)
}

// sessionKeyStepError 统一第 1/2 步的失败错误：401/403 明确提示 sessionKey
// 失效，其余带状态码与响应片段（截断 200 字符）。
func sessionKeyStepError(step string, status int, body []byte) error {
	msg := fmt.Sprintf("Claude sessionKey 换取第 %s 步失败（%d）：%s",
		step, status, truncate(strings.TrimSpace(string(body)), 200))
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth,
			"Claude sessionKey 换取第 %s 步失败（%d）：sessionKey 已过期，请重新从浏览器复制。%s",
			step, status, truncate(strings.TrimSpace(string(body)), 200)).WithProvider(channelID)
	}
	return ir.NewAPIError(ir.ErrCodeUpstreamFailed, msg).WithProvider(channelID)
}

// generatePKCE 生成 PKCE 参数（RFC 7636，与 relay-panel 同款）：
// code_verifier 为 32 随机字节的 base64url（43 字符，满足 43-128 约束），
// code_challenge 为其 SHA-256 的 base64url（S256）。
func generatePKCE() (verifier, challenge string, err error) {
	verifier, err = randomB64URL(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// randomB64URL 生成 n 个随机字节的 base64url（无 padding）字符串。
func randomB64URL(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ensureOAuthFromSessionKey 是 ChatStream 的懒处理接入点：
// credentials 里有 session_key 且没有 access_token 时，先走三步流换取
// OAuth 凭证并写回 env，再继续正常链路。已有 access_token（手动粘贴或
// 本机导入）时不动 —— 手动凭证优先级高于 sessionKey。
//
// 并发保护：同一账号的首次并发请求只有一把锁（参考 env 的 refreshing
// 状态语义，但锁在进程内完成，不经过账号状态机，避免影响路由可见性）。
func (p *Provider) ensureOAuthFromSessionKey(ctx context.Context, env *provider.AccountEnv) error {
	if strings.TrimSpace(env.GetCredential("session_key")) == "" ||
		strings.TrimSpace(env.GetCredential("access_token")) != "" {
		return nil
	}
	unlock := p.lockSessionKeyExchange(env.AccountID)
	defer unlock()
	// 拿到锁后 double-check：并发对手可能已完成换取。
	if strings.TrimSpace(env.GetCredential("access_token")) != "" {
		return nil
	}
	return p.exchangeSessionKeyIntoEnv(ctx, env)
}

// exchangeSessionKeyIntoEnv 执行换取并把结果写回 env。
// 写回模式与 auth.go 的 refreshAccessToken 一致：即使上层忽略 draft，
// 账号凭证也已更新。session_key 原值保留（换来的 token 失效后可再换）。
func (p *Provider) exchangeSessionKeyIntoEnv(ctx context.Context, env *provider.AccountEnv) error {
	client := env.Client()
	draft, err := exchangeSessionKey(ctx, client, strings.TrimSpace(env.GetCredential("session_key")))
	if err != nil {
		return err
	}
	for k, v := range draft.Credentials {
		env.SetCredential(k, v)
	}
	return nil
}

// lockSessionKeyExchange 返回按账号隔离的互斥锁的解锁函数。
func (p *Provider) lockSessionKeyExchange(accountID string) func() {
	v, _ := p.sessionKeyLocks.LoadOrStore(accountID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
