package transducer

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

func TestWriteErrorRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	err := ir.NewAPIError(ir.ErrCodeUpstreamRateLimit, "上游限流").WithStatus(http.StatusTooManyRequests).WithRetryAfter(7 * time.Second)
	WriteError(rec, ir.ProtocolOpenAIChat, err)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "7" {
		t.Fatalf("Retry-After = %q", got)
	}
}

func TestWriteErrorNoRetryAfterOnBadRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	err := ir.NewAPIError(ir.ErrCodeBadRequest, "坏请求").WithStatus(http.StatusBadRequest).WithRetryAfter(5 * time.Second)
	WriteError(rec, ir.ProtocolOpenAIChat, err)
	if rec.Header().Get("Retry-After") != "" {
		t.Fatalf("400 不应带 Retry-After: %q", rec.Header().Get("Retry-After"))
	}
}

func TestWriteErrorSubSecondRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	err := ir.NewAPIError(ir.ErrCodeUpstreamRateLimit, "限流").WithStatus(http.StatusTooManyRequests).WithRetryAfter(300 * time.Millisecond)
	WriteError(rec, ir.ProtocolAnthropic, err)
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
}
