package gateway

import (
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 熔断默认开启：阈值 5 次，冷却 10 分钟封顶。
//
// 用户可以在设置里关掉保护（见 TestCircuitBreakerCanBeDisabled），
// 但默认必须是开的——它是防止"一个坏账号被反复撞"的最后一道闸。
// 这条测试锁定默认行为，以及"冷却有上限、不会变永久封禁"。
func TestCircuitBreakerDefaultOn(t *testing.T) {
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
		t.Fatal("默认应开启熔断：累计到阈值必须让账号进入冷却")
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

// 关闭熔断后，失败只记账、不再让账号进冷却。
func TestCircuitBreakerCanBeDisabled(t *testing.T) {
	off := true
	g := New(Config{
		SettingsPath:     t.TempDir() + "/settings.json",
		AccountStorePath: t.TempDir() + "/accounts.json",
		UsagePath:        "-",
		MaxRecords:       10,
		BreakerDisabled:  off,
	})
	t.Cleanup(func() { _ = g.Close(t.Context()) })

	env := provider.NewAccountEnv("acc-off", "open-design")
	g.Pool().Add(env)

	err := ir.NewAPIError(ir.ErrCodeUpstreamFailed, "upstream returned 500: internal error")
	for i := 0; i < 20; i++ {
		g.penalizeModel(env, err, "")
	}

	if !env.Available() {
		t.Fatal("关闭熔断后账号不应进入冷却")
	}
	// 失败仍然要记账，便于用户判断"到底是账号坏了还是线路坏了"。
	if env.ConsecutiveFailures() == 0 {
		t.Fatal("关闭熔断不等于不记失败次数")
	}
}

// 打开开关时，要顺手解除已有的冷却（否则新开关只对将来生效）。
func TestDisablingBreakerReleasesCoolingAccounts(t *testing.T) {
	g := New(Config{
		SettingsPath:     t.TempDir() + "/settings.json",
		AccountStorePath: t.TempDir() + "/accounts.json",
		UsagePath:        "-",
		MaxRecords:       10,
	})
	t.Cleanup(func() { _ = g.Close(t.Context()) })

	cooling := provider.NewAccountEnv("acc-cooling", "open-design")
	cooling.MarkCoolingDown(5*time.Minute, "上游限流")
	g.Pool().Add(cooling)

	if cooling.Available() {
		t.Fatal("前置条件错误：账号应处于冷却")
	}

	g.breakerOff.Store(true)
	g.releaseCoolingAccounts()

	if !cooling.Available() {
		t.Fatal("关闭熔断后应解除冷却")
	}
}

// 解除冷却只针对 cooling_down：凭证失效与额度耗尽不能顺手放过。
func TestReleaseCoolingKeepsInvalidAndDepleted(t *testing.T) {
	g := New(Config{
		SettingsPath:     t.TempDir() + "/settings.json",
		AccountStorePath: t.TempDir() + "/accounts.json",
		UsagePath:        "-",
		MaxRecords:       10,
	})
	t.Cleanup(func() { _ = g.Close(t.Context()) })

	invalid := provider.NewAccountEnv("acc-invalid", "open-design")
	invalid.MarkInvalid("token 过期")
	g.Pool().Add(invalid)

	depleted := provider.NewAccountEnv("acc-depleted", "open-design")
	depleted.MarkDepleted(time.Now().Add(time.Hour), "额度用尽")
	g.Pool().Add(depleted)

	g.releaseCoolingAccounts()

	if invalid.Available() {
		t.Fatal("凭证失效不应被熔断开关解除")
	}
	if depleted.Available() {
		t.Fatal("额度耗尽不应被熔断开关解除")
	}
}
