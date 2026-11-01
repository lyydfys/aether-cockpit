package provider

import (
	"testing"
	"time"
)

func TestTryAcquireRespectsCap(t *testing.T) {
	e := NewAccountEnv("a1", "p1")
	var releases []func()
	for i := 0; i < 3; i++ {
		r, ok := e.TryAcquire(3)
		if !ok {
			t.Fatalf("第 %d 次占位应成功", i+1)
		}
		releases = append(releases, r)
	}
	if _, ok := e.TryAcquire(3); ok {
		t.Fatal("超过上限应拒绝")
	}
	if e.Inflight() != 3 {
		t.Fatalf("Inflight = %d, want 3", e.Inflight())
	}

	// release 必须幂等：多条退出路径都可能调用它。
	releases[0]()
	releases[0]()
	if e.Inflight() != 2 {
		t.Fatalf("重复释放后 Inflight = %d, want 2", e.Inflight())
	}
	if _, ok := e.TryAcquire(3); !ok {
		t.Fatal("释放后应能再占位")
	}
}

func TestTryAcquireUnlimited(t *testing.T) {
	e := NewAccountEnv("a1", "p1")
	for i := 0; i < 100; i++ {
		if _, ok := e.TryAcquire(0); !ok {
			t.Fatalf("max=0 应不限并发，第 %d 次被拒", i+1)
		}
	}
	if e.Inflight() != 100 {
		t.Fatalf("Inflight = %d, want 100", e.Inflight())
	}
}

func TestAllowRequestTokenBucket(t *testing.T) {
	e := NewAccountEnv("a1", "p1")
	now := time.Now()
	for i := 0; i < 5; i++ {
		if !e.AllowRequest(5, now) {
			t.Fatalf("桶容量内第 %d 次应放行", i+1)
		}
	}
	if e.AllowRequest(5, now) {
		t.Fatal("超过每分钟上限应拒绝")
	}
	// 60 秒回填整桶：13 秒 ≈ 1.08 个令牌，至少放行一次。
	later := now.Add(13 * time.Second)
	if !e.AllowRequest(5, later) {
		t.Fatal("回填后应放行")
	}
	if !e.AllowRequest(0, later) {
		t.Fatal("rpm=0 表示不限")
	}
}

func TestNoteLatencyEWMA(t *testing.T) {
	e := NewAccountEnv("a1", "p1")
	if _, _, ok := e.Latency(); ok {
		t.Fatal("初始不应有时延样本")
	}
	// 没有首字节 / 没有输出的观测应被忽略，不能把估计带偏。
	e.NoteLatency(0, 100, 1000)
	e.NoteLatency(200, 0, 1000)
	if _, _, ok := e.Latency(); ok {
		t.Fatal("无效观测不应产生样本")
	}

	e.NoteLatency(200, 100, 1200) // 吞吐 = 100 / 1s
	ttft, tps, ok := e.Latency()
	if !ok || ttft != 200 || tps != 100 {
		t.Fatalf("首个样本 = (%v, %v, %v), want (200, 100, true)", ttft, tps, ok)
	}
	// α=0.3：TTFT = 0.3*400 + 0.7*200 = 260
	e.NoteLatency(400, 100, 1400)
	ttft, _, _ = e.Latency()
	if ttft < 259.9 || ttft > 260.1 {
		t.Fatalf("EWMA TTFT = %v, want 260", ttft)
	}
}
