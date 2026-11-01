package minimax

import (
	"net/http"
	"testing"

	"aether-cockpit/internal/core/ir"
)

// TestMapUpstreamErrorRetryAfter 429 的限流窗口要进 ir.APIError.RetryAfter。
func TestMapUpstreamErrorRetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "42")
	ae := mapUpstreamError("minimax-code", http.StatusTooManyRequests, `{"error":"rate limited"}`, h)
	if ae == nil || ae.Code != ir.ErrCodeUpstreamRateLimit || !ae.Retryable {
		t.Fatalf("错误映射 = %+v", ae)
	}
	if s := ae.RetryAfter.Seconds(); s < 40 || s > 44 {
		t.Fatalf("RetryAfter = %v, want ≈42s", ae.RetryAfter)
	}
}
