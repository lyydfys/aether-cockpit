package openaicompat

import (
	"net/http"
	"strings"
	"testing"
)

// 401/403/404/402/429/5xx 六种失败的"可重试性"与文案必须区分开。
//
// 这直接决定调度器行为：把 404（模型不存在）标成可重试，会在所有账号上
// 挨个撞一遍；把 429 标成不可重试，又会把一次限流当成账号失效。
func TestClassifyUpstream(t *testing.T) {
	cases := []struct {
		name      string
		code      int
		body      string
		wantCode  string
		retryable bool
		contains  string
	}{
		{"401 凭证无效", 401, `{"error":{"message":"invalid api key"}}`, "upstream_auth_failed", false, "401"},
		{"403 缺权限", 403, `{"error":{"message":"model not allowed"}}`, "upstream_auth_failed", false, "403"},
		{"403 封禁", 403, `{"message":"BANNED: account suspended"}`, "upstream_auth_failed", false, "封禁"},
		{"404 路径/模型不存在", 404, "404 page not found", "bad_request", false, "404"},
		{"402 余额不足", 402, `{"error":"insufficient balance"}`, "upstream_failed", true, "402"},
		{"429 限流", 429, `{"error":"rate limited"}`, "upstream_rate_limited", true, "429"},
		{"500 上游故障", 500, "internal error", "upstream_failed", true, "500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyUpstream(tc.code, tc.body, http.Header{})
			if string(got.Code) != tc.wantCode {
				t.Fatalf("code = %q，期望 %q（%s）", got.Code, tc.wantCode, got.Message)
			}
			if got.Retryable != tc.retryable {
				t.Fatalf("retryable = %v，期望 %v（%s）", got.Retryable, tc.retryable, got.Message)
			}
			if !strings.Contains(got.Message, tc.contains) {
				t.Fatalf("文案应包含 %q，得到 %q", tc.contains, got.Message)
			}
		})
	}
}

// 状态码要写进错误里：账本的 err_type / err_code 统计依赖它。
func TestClassifyUpstreamKeepsStatus(t *testing.T) {
	for _, code := range []int{401, 403, 404, 402, 429} {
		if got := classifyUpstream(code, "", http.Header{}); got.HTTPStatus != code {
			t.Errorf("HTTP %d 的 HTTPStatus 字段 = %d", code, got.HTTPStatus)
		}
	}
}

// 上游回 HTML（nginx / Cloudflare 页面）时不要把整段 HTML 塞进错误。
func TestClassifyUpstreamHTMLBody(t *testing.T) {
	got := classifyUpstream(404, "<html><head><title>404</title></head><body>nginx</body></html>", http.Header{})
	if strings.Contains(got.Message, "<html") {
		t.Fatalf("不应回显 HTML：%q", got.Message)
	}
	if !strings.Contains(got.Message, "HTML") {
		t.Fatalf("应提示这是 HTML 页面：%q", got.Message)
	}
}

// 429 要带上 Retry-After 提示（上游给了就要尊重）。
func TestClassifyUpstreamRetryAfter(t *testing.T) {
	hdr := http.Header{}
	hdr.Set("Retry-After", "30")
	got := classifyUpstream(429, "", hdr)
	if got.RetryAfter == 0 {
		t.Fatalf("应解析 Retry-After，得到 %+v", got)
	}
}
