package openaicompat

import (
	"net/http"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 上游 HTTP 错误 → 内部错误的统一分类。
//
// 为什么集中在一个函数里：OpenAI 兼容渠道占绝大多数，它们的失败语义其实
// 只有六种，但过去把 401/403 混成"鉴权失败"、把 404 当"上游返回 404"、
// 把 402 标成可重试，导致：
//   - 用户看不出"是我的 Key 错了"还是"这个模型不存在"；
//   - 调度器在"模型不存在"这种确定性错误上反复换账号重试；
//   - 429 之外的错误没有 status，账本里的 err_type 统计不出来。
//
// 分类依据（业界通行）：
//
//	401 → 凭证无效/过期（换账号无意义，换 Key 才有意义）
//	403 → 权限不足或账号被封禁（终态，重试无用）
//	404 → 路径/模型不存在（确定性，不重试）
//	402 → 余额不足（同一账号重试无用，换账号有意义 → 可重试）
//	429 → 限流（可重试，尊重 Retry-After）
//	5xx → 上游故障（可重试）
func classifyUpstream(code int, body string, hdr http.Header) *ir.APIError {
	msg := summarizeUpstreamBody(body)
	switch {
	case code == http.StatusUnauthorized:
		return ir.NewAPIError(ir.ErrCodeUpstreamAuth, "上游拒绝凭证（401）：API Key 无效或已过期").
			WithStatus(code)
	case code == http.StatusForbidden:
		if isBannedBody(body) {
			return ir.NewAPIError(ir.ErrCodeUpstreamAuth, "上游账号被封禁（403）：请停止使用该账号").
				WithStatus(code)
		}
		return ir.NewAPIError(ir.ErrCodeUpstreamAuth, "上游拒绝访问（403）：该 Key 没有这个模型/接口的权限").
			WithStatus(code)
	case code == http.StatusNotFound:
		return ir.NewAPIError(ir.ErrCodeBadRequest,
			"上游没有这个接口或模型（404）：请检查 Base URL 是否带正确版本路径、模型名是否存在"+msg).
			WithStatus(code)
	case code == http.StatusTooManyRequests:
		return ir.NewAPIError(ir.ErrCodeUpstreamRateLimit, "上游限流（429）"+msg).
			WithRetryable(true).WithStatus(code).
			WithRetryAfter(provider.RetryAfterHint(hdr, body))
	case code == http.StatusPaymentRequired:
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游账户余额不足（402）：换一个账号可能可用"+msg).
			WithRetryable(true).WithStatus(code)
	case code == http.StatusBadRequest || code == http.StatusUnprocessableEntity:
		return ir.NewAPIError(ir.ErrCodeBadRequest, "上游拒绝请求（"+itoaStatus(code)+"）"+msg).
			WithStatus(code)
	case code >= 500:
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游故障（"+itoaStatus(code)+"）"+msg).
			WithRetryable(true).WithStatus(http.StatusBadGateway)
	default:
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游返回 "+itoaStatus(code)+msg).
			WithStatus(code)
	}
}

// isBannedBody 判断 403 的响应体是不是"封禁"而非"缺权限"。
//
// 这两者处理方式完全不同：缺权限换个模型就行，封禁必须停用账号，
// 否则会一直撞在同一个终态错误上。
func isBannedBody(body string) bool {
	b := strings.ToLower(body)
	for _, kw := range []string{"banned", "suspended", "deactivated", "封禁", "已停用", "account disabled"} {
		if strings.Contains(b, kw) {
			return true
		}
	}
	return false
}

// summarizeUpstreamBody 把响应体压成一句可读的补充说明。
//
// 错误体常常是 HTML（nginx 404、Cloudflare 页面），整段塞进错误里没意义，
// 还容易把用户吓到；只取 JSON 里的 message 或截断后的纯文本。
func summarizeUpstreamBody(body string) string {
	s := strings.TrimSpace(body)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "<") {
		return "（上游返回了 HTML 页面，通常是地址不对或路径不存在）"
	}
	// 优先取 JSON 里的 message/error 文案。
	if i := strings.Index(s, `"message"`); i >= 0 {
		if v := jsonStringAfter(s, i); v != "" {
			return "：" + truncateForError(v)
		}
	}
	return "：" + truncateForError(s)
}

// jsonStringAfter 粗略取出 `"message": "..."` 的值（不引入完整解析，
// 因为上游错误体经常不是合法 JSON）。
func jsonStringAfter(s string, idx int) string {
	rest := s[idx:]
	i := strings.Index(rest, ":")
	if i < 0 {
		return ""
	}
	rest = strings.TrimSpace(rest[i+1:])
	if !strings.HasPrefix(rest, `"`) {
		return ""
	}
	rest = rest[1:]
	if end := strings.Index(rest, `"`); end > 0 {
		return rest[:end]
	}
	return ""
}

func truncateForError(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// itoaStatus 把状态码变成字符串（避免为一行 fmt.Sprintf 引入依赖）。
func itoaStatus(code int) string {
	if code <= 0 {
		return "0"
	}
	var b [4]byte
	i := len(b)
	for code > 0 && i > 0 {
		i--
		b[i] = byte('0' + code%10)
		code /= 10
	}
	return string(b[i:])
}
