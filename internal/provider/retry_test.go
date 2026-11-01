package provider

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		hdr  map[string]string
		want time.Duration
		zero bool
	}{
		{name: "无头", hdr: nil, zero: true},
		{name: "Retry-After 秒", hdr: map[string]string{"Retry-After": "30"}, want: 30 * time.Second},
		{name: "Retry-After 过小被抬到下限", hdr: map[string]string{"Retry-After": "1"}, want: retryAfterFloor},
		{name: "Retry-After 过大被压到上限", hdr: map[string]string{"Retry-After": "259200"}, want: retryAfterCeil},
		{name: "x-ratelimit-reset-requests", hdr: map[string]string{"X-Ratelimit-Reset-Requests": "6m0s"}, want: 6 * time.Minute},
		{
			name: "anthropic 窗口重置取最近",
			hdr: map[string]string{
				"anthropic-ratelimit-unified-5h-reset": now.Add(5 * time.Hour).UTC().Format(time.RFC3339),
				"anthropic-ratelimit-unified-7d-reset": now.Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339),
			},
			want: 5 * time.Hour,
		},
		{
			name: "x-ratelimit-reset epoch 毫秒",
			hdr:  map[string]string{"X-Ratelimit-Reset": "1700000000000"},
			zero: true, // 过去的时间戳 → 视为无信息
		},
		{
			name: "x-ratelimit-reset 未来 epoch 秒",
			hdr:  map[string]string{"X-Ratelimit-Reset": strconv.FormatInt(now.Add(90*time.Second).Unix(), 10)},
			want: 90 * time.Second,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range tc.hdr {
				h.Set(k, v)
			}
			got := ParseRetryAfter(h)
			if tc.zero {
				if got != 0 {
					t.Fatalf("got %v, want 0", got)
				}
				return
			}
			// 时间型头有秒级抖动，允许 ±5s。
			if got < tc.want-5*time.Second || got > tc.want+5*time.Second {
				t.Fatalf("got %v, want ≈%v", got, tc.want)
			}
		})
	}
}

// 上游只把重置时间写在响应体时也要认（Antigravity 周配额就是这种）。
func TestParseRetryAfterBody(t *testing.T) {
	cases := []struct {
		body string
		want time.Duration
		zero bool
	}{
		{body: `{"error":{"code":429,"message":"Individual quota reached. Resets in 153h35m32s."}}`, want: 153*time.Hour + 35*time.Minute + 32*time.Second},
		{body: `rate limited, try again in 30s`, want: 30 * time.Second},
		{body: `Retry after 5m`, want: 5 * time.Minute},
		{body: `resets in 1s`, want: retryAfterFloor},
		{body: `resets in 400h`, want: bodyHintCeil},
		{body: `no hint here`, zero: true},
		{body: ``, zero: true},
	}
	for _, tc := range cases {
		got := ParseRetryAfterBody(tc.body)
		if tc.zero {
			if got != 0 {
				t.Fatalf("body=%q got %v, want 0", tc.body, got)
			}
			continue
		}
		if got < tc.want-2*time.Second || got > tc.want+2*time.Second {
			t.Fatalf("body=%q got %v, want ≈%v", tc.body, got, tc.want)
		}
	}
}

// 头优先，头没有再退到 body。
func TestRetryAfterHintPrefersHeader(t *testing.T) {
	h := http.Header{"Retry-After": []string{"20"}}
	if got := RetryAfterHint(h, "resets in 5h"); got != 20*time.Second {
		t.Fatalf("应优先用响应头: %v", got)
	}
	if got := RetryAfterHint(http.Header{}, "resets in 5h"); got != 5*time.Hour {
		t.Fatalf("头缺失时应退到 body: %v", got)
	}
}
