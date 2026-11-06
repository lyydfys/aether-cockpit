package codebuddy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
	"aether-cockpit/internal/providers/openaicompat"
)

// CodeBuddy 对话：POST {base}/v2/chat/completions（OpenAI 兼容 SSE）。
//
// 上游除标准 Authorization 外还校验一套 OpenAI JS SDK / CLI 指纹头与会话链路头，
// 缺失会被风控判成非官方调用（业务码 11128）；国际站还强制 messages[0] 为 system。
// 参考：Coding2API src/provider/codebuddy/{client,headers}.py。
const (
	cbChatPath = "/v2/chat/completions"

	cbCLIVersion      = "2.107.0"
	cbSDKVersion      = "6.25.0"
	cbNodeVersion     = "v24.11.1"
	cbIntlCLIVersion  = "2.178.0"
	cbIntlSDKVersion  = "7.39.0"
	cbIntlNodeVersion = "v26.7.0"

	cbLeadingSystem = "You are a helpful assistant."
)

// 业务码：见参考实现 events.py。
const (
	cbCodeModelRateLimited = 6004
	cbCodeModelSaturated   = 14003
	cbCodeModelNotAllowed  = 11102
	cbCodeModelPlanBlocked = 11158
	cbCodeCreditsExhausted = 14018
	cbCodePlanExhausted    = 1005
	cbCodeAccountRestrict  = 11140
)

var cbRiskControlCodes = map[int]bool{11101: true, 11115: true, 11128: true, 11135: true}

// ChatStream 走 CodeBuddy 官方聊天接口，流式回传。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	token := strings.TrimSpace(cbFirst(env.GetCredential("token"), env.GetCredential("access_token")))
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"CodeBuddy 账号缺少 access_token；请重新登录或手动粘贴 Token").
			WithProvider(p.profile.id)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = p.profile.baseURL
	}
	intl := p.profile.id == intlProfile.id

	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = strings.TrimPrefix(req.Model, p.profile.id+"/")
	}

	payload := openaicompat.BuildChatPayload(req, model)
	decorateChatBody(payload, intl)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "CodeBuddy 请求序列化失败").WithCause(err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+cbChatPath, bytes.NewReader(body))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 CodeBuddy 请求失败").WithCause(err)
	}
	applyChatHeaders(httpReq, env, base, token, model, intl)

	resp, err := env.Client().Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "CodeBuddy 上游连接失败").
			WithCause(err).WithProvider(p.profile.id).WithRetryable(true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, cbHTTPError(p.profile.id, resp.StatusCode, string(raw), resp.Header)
	}

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		// 官方 XHR 形态下 Accept 是 application/json，个别端点会真的回整段
		// JSON；先按 Content-Type 分流，避免把 JSON 当 SSE 解析成空响应。
		if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "event-stream") {
			openaicompat.ParseChatJSON(ctx, out, resp.Body)
			return
		}
		openaicompat.ParseChatSSE(ctx, out, resp.Body)
	}()
	return out, nil
}

// decorateChatBody 按官方 CLI 的请求形态补齐字段：developer→system、
// 思考开关与 effort 缺省、include_usage，国际站补首条 system。
func decorateChatBody(payload map[string]any, intl bool) {
	payload["enable_thinking"] = true
	if s, _ := payload["reasoning_effort"].(string); strings.TrimSpace(s) == "" {
		payload["reasoning_effort"] = "medium"
	}
	if so, ok := payload["stream_options"].(map[string]any); ok {
		so["include_usage"] = true
	} else {
		payload["stream_options"] = map[string]any{"include_usage": true}
	}

	msgs, _ := payload["messages"].([]map[string]any)
	for _, m := range msgs {
		if role, _ := m["role"].(string); role == "developer" {
			m["role"] = "system"
		}
	}
	if intl {
		needLeading := true
		if len(msgs) > 0 {
			if role, _ := msgs[0]["role"].(string); role == "system" {
				needLeading = false
			}
		}
		if needLeading {
			leading := map[string]any{"role": "system", "content": cbLeadingSystem}
			payload["messages"] = append([]map[string]any{leading}, msgs...)
		}
	}
}

// applyChatHeaders 写入官方 CLI 指纹头、会话链路头与账号头。
func applyChatHeaders(req *http.Request, env *provider.AccountEnv, base, token, model string, intl bool) {
	h := req.Header
	cli, sdk, node := cbCLIVersion, cbSDKVersion, cbNodeVersion
	if intl {
		cli, sdk, node = cbIntlCLIVersion, cbIntlSDKVersion, cbIntlNodeVersion
	}

	host := ""
	if u, err := url.Parse(base); err == nil {
		host = u.Host
	}
	requestID := randomHex(16)
	convID := cbConversationUUID(cbConversationSeed(env.AccountID, token))

	h.Set("Authorization", "Bearer "+token)
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("X-Requested-With", "XMLHttpRequest")
	h.Set("x-stainless-arch", stainlessArch())
	h.Set("x-stainless-lang", "js")
	h.Set("x-stainless-os", stainlessOS())
	h.Set("x-stainless-package-version", sdk)
	h.Set("x-stainless-retry-count", "0")
	h.Set("x-stainless-runtime", "node")
	h.Set("x-stainless-runtime-version", node)
	h.Set("X-Conversation-ID", convID)
	h.Set("X-Conversation-Request-ID", randomHex(16))
	h.Set("X-Conversation-Message-ID", randomHex(16))
	h.Set("X-Request-ID", requestID)
	h.Set("X-Agent-Intent", "craft")
	h.Set("X-Agent-Purpose", "conversation")
	h.Set("X-IDE-Type", "CLI")
	h.Set("X-IDE-Name", "CLI")
	h.Set("X-IDE-Version", cli)
	h.Set("User-Agent", "CLI/"+cli+" CodeBuddy/"+cli)
	h.Set("X-Private-Data", "false")
	h.Set("X-CodeBuddy-Request", "1")
	h.Set("X-Product", "SaaS")
	// 官方 CLI 会声明自己在什么平台上跑（web/desktop）；缺这个字段的请求
	// 在对照开源实现（codebuddy2api）时就会被识别为"头不全"。
	h.Set("X-Client-Platform", "web")
	if host != "" {
		h.Set("X-Domain", host)
		req.Host = host
	}
	if intl {
		h.Set("X-Product-Version", cli)
		h.Set("X-Trace-ID", requestID)
		h.Set("X-Session-ID", convID)
		if model != "" {
			h.Set("X-Model-ID", model)
		}
	}

	uid := strings.TrimSpace(env.GetCredential("account_uid"))
	if uid == "" {
		uid = strings.TrimSpace(env.GetCredential("uid"))
	}
	if uid == "" && intl {
		uid = jwtSubject(token)
	}
	if uid != "" {
		h.Set("X-User-Id", uid)
	}
	if ent := strings.TrimSpace(env.GetCredential("enterprise_id")); ent != "" {
		h.Set("X-Enterprise-Id", ent)
		h.Set("X-Tenant-Id", ent)
	}
}

// cbConversationSeed 让同一账号的同一段对话稳定复用会话 id；换对话即换 id。
func cbConversationSeed(accountID, token string) string {
	sum := sha256.Sum256([]byte(accountID + "\x00" + token))
	return hex.EncodeToString(sum[:16])
}

var (
	cbConvMu    sync.Mutex
	cbConvSeen  = map[string]time.Time{}
	cbConvLimit = 1024
)

// cbConversationUUID 生成 UUID v1 形态的稳定会话 id：官方 CLI 的
// conversation id 带创建时间与固定 node 段，逐请求随机 v4 是可识别特征。
func cbConversationUUID(seed string) string {
	created, ok := cbConvSeen[seed]
	if !ok {
		created = time.Now()
		cbConvMu.Lock()
		if len(cbConvSeen) >= cbConvLimit {
			cbConvSeen = map[string]time.Time{}
		}
		cbConvSeen[seed] = created
		cbConvMu.Unlock()
	}
	ticks := uint64(created.UnixNano()/100) + 0x01B21DD213814000
	digest := sha256.Sum256([]byte(seed))
	clockSeq := (binary.BigEndian.Uint16(digest[:2]) & 0x3FFF) | 0x8000
	return hex8(uint32(ticks)) + "-" +
		hex4(uint16(ticks>>32)) + "-" +
		hex4(uint16((ticks>>48)&0x0FFF)|0x1000) + "-" +
		hex4(clockSeq) + "-426614174000"
}

func hex8(v uint32) string {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return hex.EncodeToString(b)
}

func hex4(v uint16) string {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return hex.EncodeToString(b)
}

func stainlessArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64"
	default:
		return "x64"
	}
}

func stainlessOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "MacOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	default:
		return "Linux"
	}
}

// jwtSubject 只解 payload 取 sub，不验签；解析失败返回空。
func jwtSubject(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return ""
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	sub, _ := claims["sub"].(string)
	return strings.TrimSpace(sub)
}

// cbHTTPError 优先解析业务码，再退化到通用状态码映射。
func cbHTTPError(providerID string, status int, raw string, hdr http.Header) *ir.APIError {
	body := strings.TrimSpace(raw)
	msg := body
	var root map[string]any
	if json.Unmarshal([]byte(raw), &root) == nil {
		if nested, ok := root["error"].(map[string]any); ok {
			root = nested
		}
		code := int(anyInt(root["code"]))
		text := cbFirst(anyString(root["displayMsg"]), anyString(root["message"]), anyString(root["msg"]))
		if text == "" {
			text = truncateText(body, 300)
		}
		if ae := cbCodeError(providerID, code, text, hdr); ae != nil {
			return ae
		}
		if text != "" {
			msg = text
		}
	}
	ae := openaicompat.MapChatHTTPError(status, msg, hdr)
	return ae.WithProvider(providerID)
}

func cbCodeError(providerID string, code int, text string, hdr http.Header) *ir.APIError {
	label := text
	switch {
	case code == cbCodeModelRateLimited || code == cbCodeModelSaturated:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "CodeBuddy 模型用量受限（%d）：%s", code, label).
			WithProvider(providerID).WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, label))
	case code == cbCodeCreditsExhausted || code == cbCodePlanExhausted:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "CodeBuddy 额度/套餐已用尽（%d）：%s", code, label).
			WithProvider(providerID)
	case code == cbCodeModelNotAllowed || code == cbCodeModelPlanBlocked:
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "CodeBuddy 当前套餐不允许该模型（%d）：%s", code, label).
			WithProvider(providerID)
	case cbRiskControlCodes[code]:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "CodeBuddy 渠道风控拦截（%d）：%s", code, label).
			WithProvider(providerID).WithRetryable(true)
	case code == cbCodeAccountRestrict:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "CodeBuddy 账号被限制（%d）：%s", code, label).
			WithProvider(providerID).WithRetryable(true)
	}
	return nil
}

func cbFirst(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func anyInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		var i int64
		_ = json.Unmarshal([]byte(n), &i)
		return i
	}
	return 0
}

func anyString(v any) string {
	s, _ := v.(string)
	return s
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
