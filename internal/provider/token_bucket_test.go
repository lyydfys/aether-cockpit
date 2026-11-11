package provider

import (
	"testing"
	"time"
)

// token 级限流：按请求数限流会被大上下文请求瞬间打爆。
func TestAllowTokensTokenBucket(t *testing.T) {
	env := NewAccountEnv("acc-tpm", "open-design")
	now := time.Now()

	const tpm = 60000 // 每分钟 6 万 token

	// 第一发：预扣 20000，还剩 40000。
	if !env.AllowTokens(tpm, 20000, now) {
		t.Fatal("首次应放行")
	}
	if !env.AllowTokens(tpm, 20000, now) {
		t.Fatal("第二次应放行")
	}
	// 第三发 20000 正好用尽。
	if !env.AllowTokens(tpm, 20000, now) {
		t.Fatal("第三次应放行")
	}
	// 第四发应被拒：桶空了。
	if env.AllowTokens(tpm, 1, now) {
		t.Fatal("桶空后应拒绝")
	}

	// 过 6 秒补充 6000 token（60000/60*6），足够一发 5000。
	if !env.AllowTokens(tpm, 5000, now.Add(6*time.Second)) {
		t.Fatal("补充后应重新放行")
	}
}

// 不限（tpm<=0）时永远放行，且不做任何记账。
func TestAllowTokensUnlimited(t *testing.T) {
	env := NewAccountEnv("acc-tpm0", "open-design")
	now := time.Now()
	for i := 0; i < 100; i++ {
		if !env.AllowTokens(0, 10_000_000, now) {
			t.Fatal("tpm=0 应表示不限")
		}
	}
	if got := env.TokenBudget(0, now); got != -1 {
		t.Fatalf("不限时 TokenBudget 应返回 -1，实际 %d", got)
	}
}

// 单发超过整桶容量时也应当被拒（而不是永远放行）。
func TestAllowTokensRejectsOversizedSingle(t *testing.T) {
	env := NewAccountEnv("acc-tpm-big", "open-design")
	now := time.Now()
	if env.AllowTokens(1000, 5000, now) {
		t.Fatal("需求超过每分钟上限时应拒绝")
	}
}

// 并发安全：多个 goroutine 同时扣配额不会超发。
func TestAllowTokensConcurrentSafety(t *testing.T) {
	env := NewAccountEnv("acc-tpm-conc", "open-design")
	now := time.Now()
	const tpm = 100
	granted := make(chan bool, 400)
	for i := 0; i < 400; i++ {
		go func() { granted <- env.AllowTokens(tpm, 1, now) }()
	}
	ok := 0
	for i := 0; i < 400; i++ {
		if <-granted {
			ok++
		}
	}
	if ok != tpm {
		t.Fatalf("应恰好放行 %d 次（桶容量），实际 %d 次", tpm, ok)
	}
}
