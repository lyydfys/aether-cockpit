package workbuddy

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

var (
	w3cTraceparent = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`)
	hex32          = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hex16          = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// UA 与 X-IDE-Version 必须同源同版本，且两个 region 都带齐新增头。
func TestApplyHeadersIdentityBothRegions(t *testing.T) {
	for _, prof := range []profile{cnProfile, intlProfile} {
		t.Run(prof.id, func(t *testing.T) {
			p := newProvider(prof)
			env := provider.NewAccountEnv("acc-"+prof.id, prof.id)
			env.Credentials = map[string]string{"token": "tk", "user_id": "uid"}
			req := httptest.NewRequest(http.MethodPost, "https://example.com/v2/chat/completions", nil)

			p.applyHeaders(req, env, "tk", "uid", &ir.UnifiedChatRequest{}, false)

			want := map[string]string{
				"X-Product":           "SaaS",
				"X-IDE-Type":          "WorkBuddy",
				"X-IDE-Name":          "WorkBuddy",
				"X-IDE-Version":       clientVersion,
				"X-Private-Data":      "true",
				"X-CodeBuddy-Request": "1",
				"X-Requested-With":    "XMLHttpRequest",
				"User-Agent":          "WorkBuddy/" + clientVersion,
			}
			for k, v := range want {
				if got := req.Header.Get(k); got != v {
					t.Fatalf("%s = %q, want %q", k, got, v)
				}
			}
			if got := req.Header.Get("User-Agent"); got != "WorkBuddy/"+req.Header.Get("X-IDE-Version") {
				t.Fatalf("UA 与 X-IDE-Version 版本不一致: UA=%q IDE=%q", got, req.Header.Get("X-IDE-Version"))
			}

			// W3C traceparent 格式 + B3/baggage 同源。
			tp := req.Header.Get("traceparent")
			if !w3cTraceparent.MatchString(tp) {
				t.Fatalf("traceparent 格式不符合 W3C: %q", tp)
			}
			parts := strings.Split(tp, "-")
			if req.Header.Get("X-B3-TraceId") != parts[1] {
				t.Fatalf("X-B3-TraceId 与 traceparent 不一致: %q vs %q", req.Header.Get("X-B3-TraceId"), parts[1])
			}
			if req.Header.Get("X-B3-SpanId") != parts[2] {
				t.Fatalf("X-B3-SpanId 与 traceparent 不一致: %q vs %q", req.Header.Get("X-B3-SpanId"), parts[2])
			}
			for _, h := range []string{"X-B3-TraceId", "X-B3-ParentSpanId", "X-B3-SpanId"} {
				wantRe := hex16
				if h == "X-B3-TraceId" {
					wantRe = hex32
				}
				if !wantRe.MatchString(req.Header.Get(h)) {
					t.Fatalf("%s 格式不对: %q", h, req.Header.Get(h))
				}
			}
			if !strings.Contains(req.Header.Get("baggage"), "codebuddy.session_id=") ||
				!strings.Contains(req.Header.Get("baggage"), "codebuddy.conversation_request_id=") {
				t.Fatalf("baggage 缺字段: %q", req.Header.Get("baggage"))
			}
			if req.Header.Get("X-Machine-ID") != "" || req.Header.Get("X-Session-ID") != "" {
				t.Fatal("X-Machine-ID / X-Session-ID 默认不应发送（官方对话请求也不带）")
			}
		})
	}
}

func TestHexID(t *testing.T) {
	uuid := "123e4567-e89b-12d3-a456-426614174000"
	if got := hexID(uuid, 32); got != "123e4567e89b12d3a456426614174000" {
		t.Fatalf("hexID(uuid,32) = %q", got)
	}
	if got := hexID(uuid, 16); got != "123e4567e89b12d3" {
		t.Fatalf("hexID(uuid,16) = %q", got)
	}
	// 非十六进制字符被剔除，长度不足用 sha256 补齐且稳定。
	a := hexID("状态-1", 32)
	b := hexID("状态-1", 32)
	if !hex32.MatchString(a) || a != b {
		t.Fatalf("短 id 应稳定补齐成 32 hex: %q %q", a, b)
	}
	if hexID("", 32) == "" {
		t.Fatal("空 id 也应产出合法 trace id")
	}
}

func TestPaceWaitAt(t *testing.T) {
	const gap = 350 * time.Millisecond
	next := map[string]time.Time{}
	now := time.Unix(1000, 0)

	if got := paceWaitAt(next, "", now, gap); got != 0 {
		t.Fatalf("空账号不应节流，got %v", got)
	}
	if got := paceWaitAt(next, "acc", now, gap); got != 0 {
		t.Fatalf("首次请求不应等待，got %v", got)
	}
	// 同一时刻的第二个请求应排到下一个时间片。
	if got := paceWaitAt(next, "acc", now, gap); got != gap {
		t.Fatalf("第二次请求应等待 %v，got %v", gap, got)
	}
	// 并发第三个请求继续顺延。
	if got := paceWaitAt(next, "acc", now, gap); got != 2*gap {
		t.Fatalf("第三次请求应等待 %v，got %v", 2*gap, got)
	}
	// 时间已越过所有时间片后不再等待。
	if got := paceWaitAt(next, "acc", now.Add(10*gap), gap); got != 0 {
		t.Fatalf("越过时间片后不应等待，got %v", got)
	}
	// 不同账号互不影响。
	if got := paceWaitAt(next, "other", now, gap); got != 0 {
		t.Fatalf("不同账号不应共享节流，got %v", got)
	}
}
