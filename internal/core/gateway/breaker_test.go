package gateway

import (
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 熔断不可关闭：这是不变量，不是可选项。
//
// 为什么不给开关：
//   - 它是防止"一个坏账号被反复撞"的最后一道闸。关掉之后，上游持续 5xx
//     的账号会被每个请求各撞一次，既拖慢整体，也会把上游的临时故障
//     放大成我们的持续故障；
//   - 超时可以按用户需要放开（中转站排队慢），但"放开等待"和"不设保护"
//     是两件事：等待时间由用户决定，熔断是系统自保。
//
// 这条测试锁定两点：阈值仍然生效；冷却时长有上限，不会变永久封禁。
func TestCircuitBreakerCannotBeDisabled(t *testing.T) {
	// 即使把首字节超时与总预算都设成"不限"，熔断依然要工作。
	g := New(Config{
		SettingsPath:     t.TempDir() + "/settings.json",
		AccountStorePath: t.TempDir() + "/accounts.json",
		UsagePath:        "-",
		MaxRecords:       10,
		MaxAttempts:      1,
		FirstByteTimeout: -1, // 不限
		RequestBudget:    -1, // 不限
	})
	t.Cleanup(func() { _ = g.Close(t.Context()) })

	env := provider.NewAccountEnv("acc-breaker", "open-design")
	g.Pool().Add(env)

	// 普通上游故障（非建连类、非边缘类）会计入失败。
	err := ir.NewAPIError(ir.ErrCodeUpstreamFailed, "upstream returned 500: internal error")

	// 阈值是 5：前 4 次不应熔断。
	for i := 0; i < 4; i++ {
		g.penalizeModel(env, err, "")
	}
	if !env.Available() {
		t.Fatal("未达阈值前不应熔断")
	}

	// 第 5 次必须熔断。
	g.penalizeModel(env, err, "")
	if env.Available() {
		t.Fatal("熔断不可关闭：累计到阈值必须让账号进入冷却")
	}

	// 冷却时长必须有上限，不能变成永久封禁。
	state, _, resetAt := env.StateDetail()
	if state != provider.StateCoolingDown {
		t.Fatalf("状态应为 cooling_down，实际 %s", state)
	}
	if resetAt.IsZero() {
		t.Fatal("冷却必须带恢复时间（否则等同于永久停用）")
	}
	// failureBackoff 上限 10 分钟，加 ±10% 抖动后不超过 11 分钟。
	if left := time.Until(resetAt); left > 11*time.Minute {
		t.Fatalf("冷却时长 %v 超过上限（应 ≤10 分钟 + 抖动）", left)
	}
}

// 建连类失败不算账号的错，因此不触发熔断——这与会话里修过的
// "代理一断所有账号集体冷却"是同一个约束，不能被熔断测试改回去。
func TestCircuitBreakerIgnoresConnFailures(t *testing.T) {
	g := New(Config{
		SettingsPath:     t.TempDir() + "/settings.json",
		AccountStorePath: t.TempDir() + "/accounts.json",
		UsagePath:        "-",
		MaxRecords:       10,
	})
	t.Cleanup(func() { _ = g.Close(t.Context()) })

	env := provider.NewAccountEnv("acc-conn2", "open-design")
	g.Pool().Add(env)

	connErr := ir.NewAPIError(ir.ErrCodeUpstreamFailed,
		"dial tcp 127.0.0.1:7892: connectex: No connection could be made because the target machine actively refused it.")
	for i := 0; i < 20; i++ {
		g.penalizeModel(env, connErr, "")
	}
	if !env.Available() {
		t.Fatal("建连失败不应触发熔断（那是网络/代理问题，不是账号问题）")
	}
}
