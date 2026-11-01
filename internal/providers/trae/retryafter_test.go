package trae

import (
	"net/http"
	"testing"

	"aether-cockpit/internal/core/ir"
)

// TestMapHTTPErrorRetryAfter HTTP 429 的限流窗口要进 ir.APIError.RetryAfter。
func TestMapHTTPErrorRetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "42")
	ae := mapHTTPError("trae", http.StatusTooManyRequests, `{"error":"rate limited"}`, h)
	if ae == nil || ae.Code != ir.ErrCodeUpstreamRateLimit || !ae.Retryable {
		t.Fatalf("错误映射 = %+v", ae)
	}
	if s := ae.RetryAfter.Seconds(); s < 40 || s > 44 {
		t.Fatalf("RetryAfter = %v, want ≈42s", ae.RetryAfter)
	}
}

// TestTraeCodeErrorRetryAfter 业务码 4011/4017 的限流也要带上窗口。
func TestTraeCodeErrorRetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "42")
	for _, code := range []int{4011, 4017} {
		ae := traeCodeError("trae", code, "rate limited", h)
		if ae == nil || ae.Code != ir.ErrCodeUpstreamRateLimit || !ae.Retryable {
			t.Fatalf("code %d 映射 = %+v", code, ae)
		}
		if s := ae.RetryAfter.Seconds(); s < 40 || s > 44 {
			t.Fatalf("code %d RetryAfter = %v, want ≈42s", code, ae.RetryAfter)
		}
	}
}
