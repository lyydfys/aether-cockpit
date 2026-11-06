package workbuddy

// 风控保护：上游判定"异常行为"时会返回 11140 / 403，并可能对**整个区域**
// 的账号做连坐限流。真实客户端遇到这种信号会先停下来，而不是继续重试——
// 继续打只会把"可疑"变成"确认滥用"。
//
// 这里做两件事：
//  1. 命中风控信号时，给该渠道加一个短暂的"冷静期"（默认 10 分钟）；
//  2. 冷静期内直接拒绝新请求并给出明确提示，而不是把每个请求都送去撞墙。

import (
	"sync"
	"time"
)

// riskPauseDefault 是命中风控后的冷静时长。
//
// 取 30 分钟：对齐参考实现对 11140/11142 的冷却档位，也给"整区域连坐限流"
// 留出足够时间。太短（如 10 分钟）会让同一个出口反复撞墙，把"可疑"升级成
// "确认滥用"；账号级冷却由网关 penalize / cooldown.go 另行处理。
const riskPauseDefault = 30 * time.Minute

var riskPauses = struct {
	mu sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

// pauseProvider 记录一次风控冷静期。
func pauseProvider(id string, d time.Duration) {
	if id == "" || d <= 0 {
		return
	}
	riskPauses.mu.Lock()
	riskPauses.at[id] = time.Now().Add(d)
	riskPauses.mu.Unlock()
}

// pausedUntil 返回该渠道的冷静期截止时间；未暂停返回零值。
func pausedUntil(id string) time.Time {
	riskPauses.mu.Lock()
	defer riskPauses.mu.Unlock()
	until, ok := riskPauses.at[id]
	if !ok {
		return time.Time{}
	}
	if time.Now().After(until) {
		delete(riskPauses.at, id)
		return time.Time{}
	}
	return until
}

// riskBlocked 判断响应体是否属于"风控/异常行为"信号。
//
// 11140 / 11142 是上游的异常行为码；403 在 WorkBuddy 上通常也来自风控而不是
// 鉴权（凭证失效是 401）。这些按风控处理，宁可少发一会儿也不要连坐。
func riskBlocked(status int, raw string) bool {
	if status == 403 {
		return true
	}
	return containsCode(raw, "11140") || containsCode(raw, "11142")
}

// containsCode 判断 JSON 里是否出现给定数字状态码（兼容有无空格两种写法）。
func containsCode(raw, code string) bool {
	return indexOf(raw, `"code":`+code) >= 0 || indexOf(raw, `"code": `+code) >= 0
}

// indexOf 是 strings.Index 的小包装，避免本文件依赖整个 strings 包。
func indexOf(s, sub string) int {
	n := len(sub)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}
