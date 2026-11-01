package provider

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 退避时长的合理区间：太短等于没退避，太长多半是时间戳解析错了。
// 上限取 24h 而不是 2h——Anthropic 的 5h/7d 窗口重置是真实值，压到 2h
// 会让账号提前回到池子里，再撞一次 429。
const (
	retryAfterFloor = 5 * time.Second
	retryAfterCeil  = 24 * time.Hour
)

// ParseRetryAfter 从上游响应头推断建议退避时长，取各来源中最小的正值。
//
// 覆盖的写法（都是真实上游在用的）：
//   - Retry-After: 秒数或 HTTP-date（OpenAI / Anthropic / 通用网关）
//   - X-Ratelimit-Reset-Requests / -Tokens: Go duration（"6m0s" / "1s" / "20ms"）
//   - anthropic-ratelimit-<window>-reset: RFC3339 或 epoch 秒（5h / 7d 双窗口）
//   - X-Ratelimit-Reset: epoch 秒或毫秒
//
// 返回 0 表示上游没给任何可用信息，由调用方决定默认冷却。
func ParseRetryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	best := time.Duration(0)
	for _, d := range []time.Duration{
		retryAfterHeader(h),
		resetDurationHeader(h),
		anthropicWindowReset(h),
		genericResetHeader(h),
	} {
		if d > 0 && (best == 0 || d < best) {
			best = d
		}
	}
	if best <= 0 {
		return 0
	}
	if best < retryAfterFloor {
		return retryAfterFloor
	}
	if best > retryAfterCeil {
		return retryAfterCeil
	}
	return best
}

// bodyHintPatterns 是"上游把重置时间写在错误文案里"的常见写法。
// 真实案例：Antigravity 的 429 只有 body ——
//
//	"Individual quota reached. ... Resets in 153h35m32s."
//
// 只认响应头时我们只能退回默认 2 分钟，等于对着 6 天的窗口反复撞墙。
var bodyHintPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)reset[s]?\s+in\s+((?:\d+\s*[hms]\s*)+)`),
	regexp.MustCompile(`(?i)try again in\s+((?:\d+\s*[hms]\s*)+)`),
	regexp.MustCompile(`(?i)retry after\s+((?:\d+\s*[hms]\s*)+)`),
}

// bodyHintCeil 是"上游明确说出重置时间"时的上限：允许到 7 天。
// 与响应头共用 24h 上限会把周配额（153h）截成 1 天，白白多撞 6 次。
const bodyHintCeil = 168 * time.Hour

// ParseRetryAfterBody 从错误响应体里解析重置时长；解析不到返回 0。
func ParseRetryAfterBody(body string) time.Duration {
	if strings.TrimSpace(body) == "" {
		return 0
	}
	for _, re := range bodyHintPatterns {
		m := re.FindStringSubmatch(body)
		if len(m) < 2 {
			continue
		}
		raw := strings.ReplaceAll(m[1], " ", "")
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			continue
		}
		if d < retryAfterFloor {
			d = retryAfterFloor
		}
		if d > bodyHintCeil {
			d = bodyHintCeil
		}
		return d
	}
	return 0
}

// RetryAfterHint 先看响应头，再看响应体文案，返回建议的冷却时长（0 = 都没给）。
func RetryAfterHint(h http.Header, body string) time.Duration {
	if d := ParseRetryAfter(h); d > 0 {
		return d
	}
	return ParseRetryAfterBody(body)
}

func retryAfterHeader(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func resetDurationHeader(h http.Header) time.Duration {
	for _, k := range []string{"X-Ratelimit-Reset-Requests", "X-Ratelimit-Reset-Tokens"} {
		v := strings.TrimSpace(h.Get(k))
		if v == "" {
			continue
		}
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 0
}

func anthropicWindowReset(h http.Header) time.Duration {
	var best time.Duration
	for k, vals := range h {
		lk := strings.ToLower(k)
		if !strings.HasPrefix(lk, "anthropic-ratelimit-") || !strings.HasSuffix(lk, "-reset") {
			continue
		}
		if len(vals) == 0 {
			continue
		}
		v := strings.TrimSpace(vals[0])
		var d time.Duration
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			d = time.Until(t)
		} else if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			if n > 1e12 { // 毫秒
				d = time.Until(time.UnixMilli(n))
			} else {
				d = time.Until(time.Unix(n, 0))
			}
		}
		if d > 0 && (best == 0 || d < best) {
			best = d
		}
	}
	return best
}

func genericResetHeader(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("X-Ratelimit-Reset"))
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	if n > 1e12 { // 毫秒时间戳
		return time.Until(time.UnixMilli(n))
	}
	return time.Until(time.Unix(n, 0))
}
