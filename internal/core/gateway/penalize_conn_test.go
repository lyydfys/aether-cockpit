package gateway

import (
	"context"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 建连失败不能惩罚账号。
//
// 事故：代理一断，所有账号各失败几轮，满 5 次集体进冷却，界面显示
// "账号当前不可用"，而用户看到的额度明明是够的——因为问题在网络层，
// 跟账号额度毫无关系。
func TestConnFailureDoesNotPenalizeAccount(t *testing.T) {
	g := newPenalizeGateway(t)
	env := provider.NewAccountEnv("acc-conn", "open-design")

	err := ir.NewAPIError(ir.ErrCodeUpstreamFailed,
		`dial tcp 127.0.0.1:7892: connectex: No connection could be made because the target machine actively refused it.`)

	// 连续 10 次建连失败也不该触发熔断（阈值是 5）。
	for i := 0; i < 10; i++ {
		g.penalizeModel(env, err, "")
	}

	if got := env.ConsecutiveFailures(); got != 0 {
		t.Fatalf("建连失败不应累加失败计数，实际累计 %d 次", got)
	}
	if !env.Available() {
		t.Fatal("建连失败不应把账号打成不可用")
	}
}

// 对照：真正的上游故障仍应触发熔断，否则坏账号永远不休息。
func TestRealFailureStillPenalizes(t *testing.T) {
	g := newPenalizeGateway(t)
	env := provider.NewAccountEnv("acc-real", "open-design")

	err := ir.NewAPIError(ir.ErrCodeUpstreamFailed, "upstream returned 500: internal server error")
	for i := 0; i < 5; i++ {
		g.penalizeModel(env, err, "")
	}

	if env.Available() {
		t.Fatal("真实上游故障累计到阈值后应进入冷却")
	}
	if got := env.ConsecutiveFailures(); got < 5 {
		t.Fatalf("应累计失败次数，实际 %d", got)
	}
}

// 唤醒失败的文案要说清状态与剩余时间，不能只给"冷却/停用/失效"三个词。
func TestUnavailableReasonIsActionable(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*provider.AccountEnv)
		expect []string
	}{
		{
			name:   "冷却中要带剩余时间",
			setup:  func(e *provider.AccountEnv) { e.MarkCoolingDown(3*time.Minute, "上游限流 429") },
			expect: []string{"冷却", "还剩", "429"},
		},
		{
			name:   "额度耗尽要带恢复时间",
			setup:  func(e *provider.AccountEnv) { e.MarkDepleted(time.Now().Add(2*time.Hour), "额度已用尽") },
			expect: []string{"额度已用尽", "恢复"},
		},
		{
			name:   "凭证失效要指向重新登录",
			setup:  func(e *provider.AccountEnv) { e.MarkInvalid("refresh token 已过期") },
			expect: []string{"重新登录", "过期"},
		},
	}
	for _, c := range cases {
		env := provider.NewAccountEnv("acc-x", "open-design")
		c.setup(env)
		if env.Available() {
			t.Fatalf("%s: 前置条件错误，账号应不可用", c.name)
		}
		got := unavailableReason(env)
		for _, want := range c.expect {
			if !strings.Contains(got, want) {
				t.Errorf("%s: 文案应包含 %q，实际 %q", c.name, want, got)
			}
		}
	}
}

// newPenalizeGateway 造一个最小网关，只够驱动 penalizeModel。
func newPenalizeGateway(t *testing.T) *Gateway {
	t.Helper()
	g := New(Config{
		MaxAttempts:      1,
		FirstByteTimeout: 2 * time.Second,
		StateTTL:         time.Minute,
		MaxRecords:       10,
	})
	t.Cleanup(func() { _ = g.Close(context.Background()) })
	return g
}
