package ir

import (
	"fmt"
	"strings"
	"time"
)

// APIError 是全系统统一的错误类型。
//
// Code 决定出站时翻译成哪个下游协议的哪个错误形状（HTTP 状态码 + body 结构），
// Retryable 决定路由层是否可以换账号重试。
type APIError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	ProviderID string `json:"provider_id,omitempty"`
	Retryable  bool   `json:"retryable,omitempty"`
	// HTTPStatus 建议的下游状态码，0 表示由映射表推导。
	HTTPStatus int `json:"-"`
	// RetryAfter 是上游给出的建议退避时长（来自 Retry-After / 限流窗口重置头）。
	// 0 表示上游没给，路由层用默认冷却。
	RetryAfter time.Duration `json:"-"`
	cause      error
}

// WithRetryAfter 记录上游建议的退避时长。
func (e *APIError) WithRetryAfter(d time.Duration) *APIError {
	if d > 0 {
		e.RetryAfter = d
	}
	return e
}

// Cause 返回包装的底层错误（可为 nil）。
func (e *APIError) Cause() error { return e.cause }

// PublicMessage 是发给下游客户端的可见文案。
// 网络类失败（upstream_failed / upstream_rate_limited）附上底层原因，
// 单行、截断到 180 字符；鉴权与内部错误不带 cause，避免泄露内部细节。
func (e *APIError) PublicMessage() string {
	if e.cause == nil || (e.Code != ErrCodeUpstreamFailed && e.Code != ErrCodeUpstreamRateLimit) {
		return e.Message
	}
	detail := strings.Join(strings.Fields(e.cause.Error()), " ")
	if detail == "" {
		return e.Message
	}
	if rs := []rune(detail); len(rs) > 180 {
		detail = string(rs[:180]) + "…"
	}
	if strings.Contains(e.Message, detail) {
		return e.Message
	}
	return e.Message + "：" + detail
}

// NewAPIError 构造一个错误。
func NewAPIError(code, msg string) *APIError {
	return &APIError{Code: code, Message: msg}
}

// NewAPIErrorf 构造一个带格式化的错误。
func NewAPIErrorf(code, format string, args ...any) *APIError {
	return &APIError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithCause 附加底层原因（不参与对下游的暴露，仅用于日志）。
func (e *APIError) WithCause(err error) *APIError {
	e.cause = err
	return e
}

// WithProvider 标注出错的渠道，便于用户定位。
func (e *APIError) WithProvider(id string) *APIError {
	e.ProviderID = id
	return e
}

// WithRetryable 标记是否可换账号重试。
func (e *APIError) WithRetryable(v bool) *APIError {
	e.Retryable = v
	return e
}

// WithStatus 指定建议的下游 HTTP 状态码。
func (e *APIError) WithStatus(s int) *APIError {
	e.HTTPStatus = s
	return e
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		return e.Code + ": " + e.Message + " (" + e.cause.Error() + ")"
	}
	return e.Code + ": " + e.Message
}

// Unwrap 支持 errors.Is / errors.As 追溯底层错误。
func (e *APIError) Unwrap() error { return e.cause }

// StatusCode 返回建议的下游状态码。
func (e *APIError) StatusCode() int {
	if e.HTTPStatus != 0 {
		return e.HTTPStatus
	}
	switch e.Code {
	case ErrCodeBadRequest:
		return 400
	case ErrCodeUpstreamAuth:
		return 401
	case ErrCodeNoProvider, ErrCodeNoAccount, ErrCodePrivacyUnavailable:
		return 503
	case ErrCodePrivacyBlocked:
		// 用户的脱敏策略拦下的请求：这是策略拒绝，不是服务不可用。
		return 403
	case ErrCodePreviousResponseNotFound:
		return 400
	case ErrCodeUpstreamRateLimit:
		return 429
	case ErrCodeUnsupported:
		return 400
	case ErrCodeUpstreamFailed:
		return 502
	default:
		return 500
	}
}

// AsAPIError 尝试把任意 error 归一化为 APIError。
func AsAPIError(err error) *APIError {
	if err == nil {
		return nil
	}
	if ae, ok := err.(*APIError); ok {
		return ae
	}
	return NewAPIError(ErrCodeInternal, err.Error()).WithCause(err)
}
