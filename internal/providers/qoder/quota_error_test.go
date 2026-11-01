package qoder

import (
	"net/http"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 账号额度耗尽时上游返回 400 + agent_router 无可用流程节点；必须映射成
// 额度耗尽（429），否则用户只看到一条像 bug 的 upstream_failed。
func TestMapHTTPErrorQuotaExhausted(t *testing.T) {
	body := `{"code":"400","message":"[FAIL]node:agent_router msg:None flow nodes found for router agent_router"}`
	ae := mapHTTPError(http.StatusBadRequest, body, nil)
	if ae.Code != ir.ErrCodeUpstreamRateLimit {
		t.Fatalf("code = %s", ae.Code)
	}
	if !strings.Contains(ae.Message, "额度已用尽") {
		t.Fatalf("message = %q", ae.Message)
	}
	if ae.HTTPStatus != http.StatusTooManyRequests {
		t.Fatalf("http status = %d", ae.HTTPStatus)
	}
}

func TestMapHTTPErrorPlainBadRequestStaysUpstreamFailed(t *testing.T) {
	ae := mapHTTPError(http.StatusBadRequest, `{"message":"bad payload"}`, nil)
	if ae.Code != ir.ErrCodeUpstreamFailed {
		t.Fatalf("code = %s", ae.Code)
	}
}
