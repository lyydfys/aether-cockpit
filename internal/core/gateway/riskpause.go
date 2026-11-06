package gateway

// 跨渠道的风控保护。
//
// 背景：多数逆向渠道（WorkBuddy / Qoder / Trae / CodeBuddy / Claude 订阅…）
// 在上游判定"异常行为"时返回 403 或带风控码的 400/429。此时**继续重试**是最
// 糟的应对：它把"可疑"升级成"确认滥用"，还可能连坐同区域/同设备的其它账号。
//
// 单个渠道内部可以做更细的保护（例如 workbuddy 包里的 pauseProvider），
// 但网关这一层需要一条**通用底线**：识别到风控信号就暂停该渠道一段时间，
// 并且不要把账号标成"凭证失效"（那是 401 的语义，标错会让用户误以为要重新登录）。

import (
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
)

// riskPauseDuration 是通用风控冷静期。
//
// 10 分钟：足够打断"连续撞墙"，又不至于让用户以为渠道坏了。
const riskPauseDuration = 10 * time.Minute

// riskPauseWords 是各渠道风控文案里常见的词。命中即视为风控，而不是鉴权失败。
//
// 刻意保守：只收"明确表示账号/行为被限制"的词，避免把普通 403（例如模型无权限）
// 误判成风控而暂停整个渠道。
var riskPauseWords = []string{
	"risk", "abnormal", "suspicious", "abuse",
	"banned", "suspended", "restricted",
	"风控", "异常行为", "封禁", "滥用",
	"11140", "11128",
}

type riskPauseState struct {
	mu   sync.Mutex
	byID map[string]time.Time
}

var riskPauses = riskPauseState{byID: map[string]time.Time{}}

// riskPauseUntil 返回该渠道的冷静期截止时间；未暂停返回零值。
func riskPauseUntil(providerID string) time.Time {
	riskPauses.mu.Lock()
	defer riskPauses.mu.Unlock()
	until, ok := riskPauses.byID[providerID]
	if !ok {
		return time.Time{}
	}
	if time.Now().After(until) {
		delete(riskPauses.byID, providerID)
		return time.Time{}
	}
	return until
}

// pauseRisk 让该渠道进入冷静期。
func pauseRisk(providerID string) {
	if providerID == "" {
		return
	}
	riskPauses.mu.Lock()
	riskPauses.byID[providerID] = time.Now().Add(riskPauseDuration)
	riskPauses.mu.Unlock()
}

// isRiskSignal 判断上游错误是否属于"账号/行为被限制"。
//
// 只看文案不看状态码：不同渠道风控码不同（11140 / 403 / RISK_CONTROL），
// 但文案里一定有迹可循。
func isRiskSignal(err *ir.APIError) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Message)
	for _, w := range riskPauseWords {
		if strings.Contains(msg, strings.ToLower(w)) {
			return true
		}
	}
	return false
}
