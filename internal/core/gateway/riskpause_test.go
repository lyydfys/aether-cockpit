package gateway

import (
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

// 风控文案识别：不同渠道的风控码不同（11140/403/RISK_CONTROL），但文案有迹可循。
func TestIsRiskSignal(t *testing.T) {
	risk := []string{
		"account risk control triggered",
		"abnormal behavior detected",
		"your account has been suspended",
		"账号存在异常行为，已限制使用",
		"请求触发风控",
		`{"code":11140,"message":"bad behavior"}`,
	}
	for _, m := range risk {
		if !isRiskSignal(ir.NewAPIError(ir.ErrCodeUpstreamAuth, m)) {
			t.Errorf("%q 应判为风控", m)
		}
	}
	plain := []string{
		"invalid api key",
		"model not found",
		"rate limit exceeded",
		"",
	}
	for _, m := range plain {
		if isRiskSignal(ir.NewAPIError(ir.ErrCodeUpstreamAuth, m)) {
			t.Errorf("%q 不该判为风控", m)
		}
	}
	if isRiskSignal(nil) {
		t.Fatal("nil 不该判为风控")
	}
}

// 冷静期内跳过该渠道；过期自动恢复。
func TestRiskPauseWindow(t *testing.T) {
	id := "test-provider-risk"
	if until := riskPauseUntil(id); !until.IsZero() {
		t.Fatal("初始不应暂停")
	}
	pauseRisk(id)
	until := riskPauseUntil(id)
	if until.IsZero() {
		t.Fatal("暂停后应有截止时间")
	}
	if d := time.Until(until); d <= 0 || d > riskPauseDuration+time.Second {
		t.Fatalf("冷静时长异常：%v", d)
	}
	// 手动把时间推到过期，模拟自然恢复。
	riskPauses.mu.Lock()
	riskPauses.byID[id] = time.Now().Add(-time.Second)
	riskPauses.mu.Unlock()
	if until := riskPauseUntil(id); !until.IsZero() {
		t.Fatal("过期后应自动恢复")
	}
}
