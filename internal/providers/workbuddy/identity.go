package workbuddy

// 客户端身份（identity）：UA 与 IDE 版本同源，外加官方桌面客户端会带的插件头
// 与链路追踪头。
//
// 依据：对官方 WorkBuddy 桌面客户端 5.9.x 请求头族的离线比对（分析过程与证据见
// docs/ANTI_BAN.md）。这里**只发官方自己会发的头**，不伪造签名、不采集设备指纹、
// 不复制他人 ID；X-Machine-ID / X-Session-ID 默认不发（官方对话请求也不带）。
//
// 为什么版本必须同源：UA 里写 5.5.6、X-IDE-Version 写 5.8.3 这种自相矛盾的组合，
// 比"版本旧但一致"更像伪造客户端。因此版本只保留一个常量来源。

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// clientVersion 是 WorkBuddy 桌面客户端版本：UA 与 X-IDE-Version 共用。
	clientVersion = "5.9.3"
	// userAgent 必须保留 WorkBuddy 前缀（上游 10085 会校验）。
	userAgent  = "WorkBuddy/" + clientVersion
	ideVersion = clientVersion
)

// setPluginHeaders 写入官方桌面客户端固定带的插件头。
//
// X-Requested-With 是 XMLHttpRequest（Ajax 语义）；此前写的 "WorkBuddy" 是
// 编造值，官方客户端不会这么发。
func setPluginHeaders(h http.Header) {
	h.Set("X-Product", "SaaS")
	h.Set("X-IDE-Type", "WorkBuddy")
	h.Set("X-IDE-Name", "WorkBuddy")
	h.Set("X-IDE-Version", ideVersion)
	h.Set("X-Private-Data", "true")
	h.Set("X-CodeBuddy-Request", "1")
	h.Set("X-Requested-With", "XMLHttpRequest")
}

// applyTraceHeaders 补上 W3C traceparent + B3 + baggage 链路头。
//
// 官方客户端的会话/请求 id 会带 trace 上下文；缺失时上游看到的是一条"没有
// 调用链"的请求，和真实客户端不一致。这里全部由本会话自己的 conversation /
// request / message id 派生，不引用任何外部值。
func applyTraceHeaders(h http.Header, conversationID, requestID, messageID string) {
	traceID := hexID(conversationID, 32)
	spanID := hexID(requestID, 16)
	parentID := hexID(messageID, 16)

	h.Set("traceparent", "00-"+traceID+"-"+spanID+"-01")
	h.Set("X-B3-TraceId", traceID)
	h.Set("X-B3-ParentSpanId", parentID)
	h.Set("X-B3-SpanId", spanID)
	h.Set("X-B3-Sampled", "1")
	h.Set("b3", traceID+"-"+parentID+"-"+spanID+"-1")
	h.Set("baggage", "codebuddy.session_id="+conversationID+
		",codebuddy.conversation_request_id="+requestID)
}

// hexID 从 id 中截取 n 位小写十六进制；不足时用 sha256 补齐，保证格式恒定。
// UUID（带连字符）会被还原成 32 位十六进制。
func hexID(id string, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) < n {
		sum := sha256.Sum256([]byte(id))
		s += hex.EncodeToString(sum[:])
	}
	return s[:n]
}

// wbPaceGap 是同账号两次上游请求的最小间隔。
//
// 官方客户端按人机交互节奏发请求；参考实现的默认档是 350ms，并明确把
// <300ms 视为"偏激进"。这里取 350ms：够打断机器式的连发，又不明显影响对话。
const wbPaceGap = 350 * time.Millisecond

var wbPace = struct {
	mu   sync.Mutex
	next map[string]time.Time
}{next: map[string]time.Time{}}

// paceWaitAt 返回本次请求需要等待的时长，并把该账号的下一个可用时刻向后推。
// now 由调用方传入（纯计算，便于单测）；并发请求会各占一个时间片。
func paceWaitAt(next map[string]time.Time, accountID string, now time.Time, gap time.Duration) time.Duration {
	if accountID == "" || gap <= 0 {
		return 0
	}
	slot := next[accountID]
	if slot.IsZero() || !now.Before(slot) {
		next[accountID] = now.Add(gap)
		return 0
	}
	wait := slot.Sub(now)
	next[accountID] = slot.Add(gap)
	return wait
}

// paceWait 是同账号请求节流入口。
func paceWait(accountID string, gap time.Duration) time.Duration {
	wbPace.mu.Lock()
	defer wbPace.mu.Unlock()
	return paceWaitAt(wbPace.next, accountID, time.Now(), gap)
}
