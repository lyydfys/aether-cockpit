package kiro

import (
	"net/http"
	"testing"

	"aether-cockpit/internal/core/ir"
)

// TestMapHTTPErrorRetryAfter 429 的限流窗口要进 ir.APIError.RetryAfter，
// 网关 penalize 在 429 时直接拿它当账号冷却时长。
func TestMapHTTPErrorRetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "42")
	ae := mapHTTPError(http.StatusTooManyRequests, `{"message":"too many requests"}`, h)
	if ae == nil || ae.Code != ir.ErrCodeUpstreamRateLimit {
		t.Fatalf("错误映射 = %+v", ae)
	}
	if s := ae.RetryAfter.Seconds(); s < 40 || s > 44 {
		t.Fatalf("RetryAfter = %v, want ≈42s", ae.RetryAfter)
	}
}
