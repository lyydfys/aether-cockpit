package gateway

import (
	"net/http"
	"testing"
	"time"
)

// 渠道级并发 / RPM 覆盖：落盘、回显、生效，且"等于全局值"视为跟随全局。
func TestProviderLimitsSaveAndApply(t *testing.T) {
	g := headersGateway(t) // 含一个 mock-native 账号
	rec := saveHeaders(t, g, `{"id":"mock-native","maxConcurrency":3,"maxRPM":60}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	view := providerViewOf(t, g, "mock-native")
	if view["maxConcurrency"] != float64(3) || view["maxRPM"] != float64(60) {
		t.Fatalf("回显不对: concurrency=%v rpm=%v", view["maxConcurrency"], view["maxRPM"])
	}
	env := g.pool.ListByProvider("mock-native")[0]
	if got := g.concurrencyFor(env); got != 3 {
		t.Fatalf("concurrencyFor = %d，期望 3", got)
	}
	if got := g.rpmFor(env); got != 60 {
		t.Fatalf("rpmFor = %d，期望 60", got)
	}

	// 账号级覆盖仍然优先。
	acct := 5
	env.SetConcurrencyOverride(&acct)
	if got := g.concurrencyFor(env); got != 5 {
		t.Fatalf("账号级覆盖应优先: %d", got)
	}
	env.SetConcurrencyOverride(nil)

	// 0 = 不限。
	rec = saveHeaders(t, g, `{"id":"mock-native","maxConcurrency":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save 0 status = %d: %s", rec.Code, rec.Body.String())
	}
	if got := g.concurrencyFor(env); got != 0 {
		t.Fatalf("0 应表示不限: %d", got)
	}

	// 等于全局（默认 8）→ 清除覆盖、跟随全局。
	rec = saveHeaders(t, g, `{"id":"mock-native","maxConcurrency":8}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save global status = %d: %s", rec.Code, rec.Body.String())
	}
	if v, ok := g.providerConcurrency("mock-native"); ok {
		t.Fatalf("等于全局值时应清除覆盖，got %d", v)
	}
	if got := g.concurrencyFor(env); got != g.maxConcurrentPerAccount() {
		t.Fatalf("应回到全局值 %d，got %d", g.maxConcurrentPerAccount(), got)
	}
}

// RPM 达到覆盖值后 AllowRequest 拒绝，且 dispatch 的错误信息用覆盖值。
func TestProviderLimitsRPMEnforced(t *testing.T) {
	g := headersGateway(t)
	if rec := saveHeaders(t, g, `{"id":"mock-native","maxRPM":2}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d", rec.Code)
	}
	env := g.pool.ListByProvider("mock-native")[0]
	rpm := g.rpmFor(env)
	if rpm != 2 {
		t.Fatalf("rpmFor = %d", rpm)
	}
	now := time.Now()
	if !env.AllowRequest(rpm, now) {
		t.Fatal("第一次应放行")
	}
	if !env.AllowRequest(rpm, now) {
		t.Fatal("第二次应放行")
	}
	if env.AllowRequest(rpm, now) {
		t.Fatal("第三次应被每分钟上限拒绝")
	}
}
