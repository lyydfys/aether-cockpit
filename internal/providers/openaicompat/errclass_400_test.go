package openaicompat

import (
	"net/http"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 400 里的"额度耗尽"必须归为可重试：否则一个号没额度就会把整个请求打断，
// 而不是换下一个账号继续。
//
// 与 errclass_test.go 的 TestClassifyUpstream 并存：那里测的是"六种标准状态码"，
// 这里专测 400 这一类的细分（额度 vs 请求非法）。
func TestClassify400QuotaExhaustedIsRetryable(t *testing.T) {
	bodies := []string{
		`{"code":"400","message":"[FAIL]node:agent_router msg:None flow nodes found for router agent_router"}`,
		`{"error":{"message":"insufficient_quota"}}`,
		`{"error":{"code":14018,"message":"Credits exhausted. Please visit the link below"}}`,
		`{"message":"You exceeded your current quota, please check your plan"}`,
		`{"message":"账号额度已用尽"}`,
	}
	for _, body := range bodies {
		ae := classifyUpstream(http.StatusBadRequest, body, nil)
		if ae.Code != ir.ErrCodeUpstreamRateLimit {
			t.Errorf("body=%q 应归为 rate_limit，得到 %s", body, ae.Code)
		}
		if !ae.Retryable {
			t.Errorf("body=%q 应可重试（换账号继续）", body)
		}
	}
}

// 真正的"请求非法"仍然是确定性错误，不能因为 400 就无脑重试。
func TestClassify400PlainBadRequestStaysDeterministic(t *testing.T) {
	bodies := []string{
		`{"message":"bad payload"}`,
		`{"error":{"message":"invalid model name"}}`,
		`{"message":"quota_for_model_not_allowed"}`, // 权限，不是额度
		`{"message":"temperature must be <= 2"}`,
	}
	for _, body := range bodies {
		ae := classifyUpstream(http.StatusBadRequest, body, nil)
		if ae.Code != ir.ErrCodeBadRequest {
			t.Errorf("body=%q 应保持 bad_request，得到 %s", body, ae.Code)
		}
		if ae.Retryable {
			t.Errorf("body=%q 不该标成可重试", body)
		}
	}
}

// 空响应体按请求非法处理（没有证据说明是额度问题）。
func TestClassify400EmptyBody(t *testing.T) {
	ae := classifyUpstream(http.StatusBadRequest, "", nil)
	if ae.Code != ir.ErrCodeBadRequest || ae.Retryable {
		t.Fatalf("空体应保持 bad_request 且不重试：%+v", ae)
	}
	if !strings.Contains(ae.Message, "400") {
		t.Fatalf("文案应带状态码：%q", ae.Message)
	}
}
