package gateway

import (
	"net/http"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/provider"
)

func TestFundsExhaustedHintAndPenalty(t *testing.T) {
	for _, msg := range []string{
		"上游账户余额不足（402）：Insufficient account funds",
		"upstream: insufficient funds",
		"payment required",
		"Go usage limit exceeded",
		"GoUsageLimitError",
	} {
		if !quotaExhaustedHint(msg) {
			t.Fatalf("%q 未被识别为余额耗尽", msg)
		}
	}

	env := provider.NewAccountEnv("acc-funds", "openai-compat")
	g := &Gateway{}
	g.penalize(env, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
		"上游账户余额不足（402）：Insufficient account funds"))
	if env.Available() {
		t.Fatalf("余额不足的账号应进入耗尽态: %+v", env.Snapshot())
	}
	if env.Snapshot().State != provider.StateDepleted {
		t.Fatalf("state = %v, want depleted", env.Snapshot().State)
	}
}

func TestQuotaExhaustedHonorsRetryAfter(t *testing.T) {
	env := provider.NewAccountEnv("acc-free", "opencode")
	g := &Gateway{}
	g.penalize(env, ir.NewAPIError(ir.ErrCodeUpstreamRateLimit,
		"上游限流: FreeUsageLimitError").WithRetryAfter(14*time.Hour))
	snap := env.Snapshot()
	if snap.State != provider.StateDepleted {
		t.Fatalf("state = %v, want depleted", snap.State)
	}
	if want := time.Now().Add(13 * time.Hour).Unix(); snap.ResetAt < want {
		t.Fatalf("ResetAt = %d, want >= %d（应按 Retry-After 而不是 1 小时）", snap.ResetAt, want)
	}
}

func TestAttachResetHint(t *testing.T) {
	g := &Gateway{pool: pool.New()}
	env := provider.NewAccountEnv("a", "p")
	env.MarkCoolingDown(90*time.Minute, "429")
	g.pool.Add(env)
	ae := ir.NewAPIError(ir.ErrCodeNoAccount, "没有可用账号").WithStatus(http.StatusServiceUnavailable)
	got := g.attachResetHint(ae, []modelBinding{{ProviderID: "p"}})
	if got.RetryAfter < 89*time.Minute || got.RetryAfter > 91*time.Minute {
		t.Fatalf("RetryAfter = %v", got.RetryAfter)
	}
	// 已有上游给的窗口时不覆盖。
	with := ir.NewAPIError(ir.ErrCodeUpstreamRateLimit, "429").WithRetryAfter(5 * time.Second)
	if g.attachResetHint(with, []modelBinding{{ProviderID: "p"}}).RetryAfter != 5*time.Second {
		t.Fatal("不应覆盖上游 Retry-After")
	}
	// 400 不处理。
	bad := ir.NewAPIError(ir.ErrCodeBadRequest, "坏").WithStatus(http.StatusBadRequest)
	if g.attachResetHint(bad, []modelBinding{{ProviderID: "p"}}).RetryAfter != 0 {
		t.Fatal("400 不应带 Retry-After")
	}
}
