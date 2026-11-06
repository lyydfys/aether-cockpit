package gateway

import (
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 鉴权错误带明确重试窗口时，应冷却账号而不是打进 invalid 永久态。
// 背景：WorkBuddy 14015/401 属于登录态过期（30 分钟窗口），一次抖动不应
// 逼用户重新登录。
func TestPenalizeAuthRetryAfterCoolsDown(t *testing.T) {
	g := &Gateway{}

	env := provider.NewAccountEnv("acc-auth-cool", "workbuddy-cn")
	g.penalizeModel(env, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "WorkBuddy 登录态过期（14015）").
		WithRetryAfter(30*time.Minute), "")
	if got := env.State(); got != provider.StateCoolingDown {
		t.Fatalf("带 RetryAfter 的鉴权错误应冷却，state=%s", got)
	}
	if _, _, until := env.StateDetail(); until.IsZero() || time.Until(until) > 31*time.Minute {
		t.Fatalf("冷却截止时间异常: %v", until)
	}

	env2 := provider.NewAccountEnv("acc-auth-bad", "workbuddy-cn")
	g.penalizeModel(env2, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "token revoked"), "")
	if got := env2.State(); got != provider.StateInvalid {
		t.Fatalf("无重试窗口的鉴权错误仍应判 invalid，state=%s", got)
	}
}
