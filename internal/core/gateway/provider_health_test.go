package gateway

import (
	"testing"
	"time"

	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/provider"
)

func newHealthEnv(id, pid string) *provider.AccountEnv {
	env := provider.NewAccountEnv(id, pid)
	env.HTTPClient = nil
	return env
}

// 全灭要下线，并带上最早恢复时间。
func TestJudgeProviderHealthAllDepleted(t *testing.T) {
	now := time.Now()
	a := newHealthEnv("a1", "p")
	a.MarkDepleted(now.Add(3*time.Hour), "用尽")
	b := newHealthEnv("a2", "p")
	b.MarkDepleted(now.Add(1*time.Hour), "用尽")

	alive, reason, until := judgeProviderHealth([]*provider.AccountEnv{a, b}, now.Unix())
	if alive {
		t.Fatal("全部耗尽不应算可用")
	}
	if reason != "depleted" {
		t.Fatalf("reason = %q, want depleted", reason)
	}
	if want := now.Add(1 * time.Hour).Unix(); until != want {
		t.Fatalf("until = %d, want %d（取最早恢复）", until, want)
	}
}

// 只要有一个还能用，就不能下线。
func TestJudgeProviderHealthKeepsAlive(t *testing.T) {
	now := time.Now()
	dead := newHealthEnv("a1", "p")
	dead.MarkDepleted(now.Add(time.Hour), "用尽")
	live := newHealthEnv("a2", "p")

	if alive, _, _ := judgeProviderHealth([]*provider.AccountEnv{dead, live}, now.Unix()); !alive {
		t.Fatal("有一个 ready 账号就应保持在线")
	}
}

// 冷却/刷新是临时状态，不能当成"死"。
func TestJudgeProviderHealthCoolingIsAlive(t *testing.T) {
	now := time.Now()
	env := newHealthEnv("a1", "p")
	env.MarkCoolingDown(2*time.Minute, "429")
	if alive, _, _ := judgeProviderHealth([]*provider.AccountEnv{env}, now.Unix()); !alive {
		t.Fatal("冷却中的账号不该让整条渠道下线")
	}
	env2 := newHealthEnv("a2", "p")
	env2.MarkRefreshing()
	if alive, _, _ := judgeProviderHealth([]*provider.AccountEnv{env2}, now.Unix()); !alive {
		t.Fatal("正在刷新的账号不该让整条渠道下线")
	}
}

// 凭证失效也算全灭，但不给恢复时间（只能人工重登）。
func TestJudgeProviderHealthInvalid(t *testing.T) {
	env := newHealthEnv("a1", "p")
	env.MarkInvalid("token 失效")
	alive, reason, until := judgeProviderHealth([]*provider.AccountEnv{env}, time.Now().Unix())
	if alive || reason != "invalid" || until != 0 {
		t.Fatalf("alive=%v reason=%q until=%d", alive, reason, until)
	}
}

// 手动停用的账号不参与判定：全被手动关掉时不应触发"自动下线"。
func TestJudgeProviderHealthIgnoresDisabledAccounts(t *testing.T) {
	env := newHealthEnv("a1", "p")
	env.SetDisabled(true)
	alive, reason, _ := judgeProviderHealth([]*provider.AccountEnv{env}, time.Now().Unix())
	if alive || reason != "" {
		t.Fatalf("手动停用的账号会让 reason 为空（不自动下线），实际 alive=%v reason=%q", alive, reason)
	}
}

// 到点的自动下线要立刻放行路由，不必等下一轮巡检。
func TestAutoDisabledExpires(t *testing.T) {
	g := &Gateway{pool: pool.New()}
	g.settings.AutoDisabledProviders = map[string]AutoDisableState{
		"p": {Reason: "depleted", Until: time.Now().Add(-time.Minute).Unix()},
	}
	if !g.providerIsEnabled("p") {
		t.Fatal("过了 Until 就应放行")
	}

	future := time.Now().Add(time.Hour).Unix()
	g.settings.AutoDisabledProviders["q"] = AutoDisableState{Reason: "depleted", Until: future}
	if g.providerIsEnabled("q") {
		t.Fatal("未到 Until 应保持下线")
	}

	// 手动停用优先于自动状态。
	g.settings.DisabledProviders = []string{"p"}
	if g.providerIsEnabled("p") {
		t.Fatal("手动停用应压过自动恢复")
	}
}
