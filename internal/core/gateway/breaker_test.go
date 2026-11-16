package gateway

import (
	"strings"
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

// 403「没有这个模型的权限」是模型级问题，不能把账号判死。
//
// 真实故障：中转站对某个模型回 403，账号被标成 invalid，
// 而 invalid 不会被熔断开关放行，于是永远显示"账号不可用"，
// 但同一把 Key 换个模型其实是能用的。
func TestModelScopedForbiddenDoesNotInvalidateAccount(t *testing.T) {
	g := New(Config{
		SettingsPath:     t.TempDir() + "/settings.json",
		AccountStorePath: t.TempDir() + "/accounts.json",
		UsagePath:        "-",
		MaxRecords:       10,
	})
	t.Cleanup(func() { _ = g.Close(t.Context()) })

	env := provider.NewAccountEnv("acc-403", "custom-mca")
	g.Pool().Add(env)

	err := ir.NewAPIError(ir.ErrCodeUpstreamAuth,
		"上游拒绝访问（403）：该 Key 没有这个模型/接口的权限")
	g.penalizeModel(env, err, "deepseek-v4.1-flash")

	if !env.Available() {
		t.Fatal("403 缺权限不应把账号打成不可用（换个模型可能就能用）")
	}
	if state, _, _ := env.StateDetail(); state == provider.StateInvalid {
		t.Fatalf("不应进入 invalid 终态，实际 %s", state)
	}
	// 该模型应当被按住，避免反复撞同一个 403。
	if cooling, _ := env.ModelCoolingDown("deepseek-v4.1-flash"); !cooling {
		t.Fatal("对应的模型应被标记为休息")
	}
}

// 对照：真正的封禁仍然必须停用账号。
func TestBanned403StillInvalidatesAccount(t *testing.T) {
	g := New(Config{
		SettingsPath:     t.TempDir() + "/settings.json",
		AccountStorePath: t.TempDir() + "/accounts.json",
		UsagePath:        "-",
		MaxRecords:       10,
	})
	t.Cleanup(func() { _ = g.Close(t.Context()) })

	env := provider.NewAccountEnv("acc-banned", "custom-mca")
	g.Pool().Add(env)

	err := ir.NewAPIError(ir.ErrCodeUpstreamAuth, "上游账号被封禁（403）：请停止使用该账号")
	g.penalizeModel(env, err, "deepseek-v4.1-flash")

	if env.Available() {
		t.Fatal("封禁应停用账号")
	}
	if state, _, _ := env.StateDetail(); state != provider.StateInvalid {
		t.Fatalf("封禁应进入 invalid，实际 %s", state)
	}
}

// 用户点名测试一个"不可用"的账号时，必须真的发出请求。
//
// 否则一旦账号被误判成 invalid，界面上的「重新测试」只会回一句
// "账号当前不可用"，形成死循环：测不了 → 无法证明可用 → 恢复不了。
func TestExplicitProbeTestsUnavailableAccount(t *testing.T) {
	g := New(Config{
		SettingsPath:     t.TempDir() + "/settings.json",
		AccountStorePath: t.TempDir() + "/accounts.json",
		UsagePath:        "-",
		MaxRecords:       10,
	})
	t.Cleanup(func() { _ = g.Close(t.Context()) })

	env := provider.NewAccountEnv("acc-probe", "openai-compat")
	env.Label = "probe"
	// 指向一个必然连不上的地址：能连就说明真的发出去了。
	env.SetCredential("base_url", "http://127.0.0.1:1")
	env.SetCredential("api_key", "sk-test")
	env.MarkInvalid("上游拒绝访问（403）：该 Key 没有这个模型/接口的权限")
	g.Pool().Add(env)
	g.models.Rebuild()

	if env.Available() {
		t.Fatal("前置条件错误：账号应不可用")
	}

	// 点名测试（accountID 非空）——必须绕过可用性闸门。
	g.probeAccounts("openai-compat", "acc-probe")

	g.monitorMu.Lock()
	entry := g.monitor["openai-compat\x00acc-probe"]
	var errText string
	if entry != nil {
		errText = entry.Error
	}
	g.monitorMu.Unlock()

	if entry == nil {
		t.Fatal("点名测试应产生一条探测记录")
	}
	if strings.Contains(errText, "账号当前不可用") {
		t.Fatalf("点名测试被可用性闸门挡住了：%s", errText)
	}
	if errText == "" {
		t.Fatal("应记录真实的失败原因（连接失败），而不是空")
	}
}
