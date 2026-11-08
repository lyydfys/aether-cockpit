package gateway

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// 记账不能被落盘阻塞。
//
// 历史缺陷：flush() 在 s.mu 里直接 json.Marshal + WriteFile，而 record() 用同一把锁
// 且每个请求结束都调用 —— 每次落盘都把记账卡住（慢盘上几十到几百毫秒）。
// 现在锁内只做快照，序列化与写盘在锁外。
func TestRecordNotBlockedByFlush(t *testing.T) {
	dir := t.TempDir()
	s := newUsageStore(filepath.Join(dir, "usage.json"))
	s.maxDays = 7

	// 预置一些数据，让快照有实际体积。
	for i := 0; i < 500; i++ {
		s.record(usageCell{
			Date: "2026-11-08", ProviderID: "p", Model: "m",
			AccountID: "a", Requests: 1, InputTokens: 10, OutputTokens: 20,
		})
	}

	// 持续 flush（模拟周期落盘），同时高频 record，测量 record 的最大阻塞时间。
	var maxBlock time.Duration
	var mu sync.Mutex
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.flush()
			}
		}
	}()

	for i := 0; i < 300; i++ {
		start := time.Now()
		s.record(usageCell{Date: "2026-11-08", ProviderID: "p", Model: "m2", AccountID: "b", Requests: 1})
		d := time.Since(start)
		mu.Lock()
		if d > maxBlock {
			maxBlock = d
		}
		mu.Unlock()
	}
	close(stop)
	wg.Wait()

	// 单条记账不该出现"等一整次写盘"量级的停顿。
	if maxBlock > 120*time.Millisecond {
		t.Fatalf("record 被阻塞了 %v，说明写盘仍在锁内", maxBlock)
	}
	t.Logf("record 最大阻塞 %v", maxBlock)
}

// 落盘后的数据必须完整可读（快照拷贝不能丢字段）。
func TestFlushSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")
	s := newUsageStore(path)
	s.maxDays = 7
	s.record(usageCell{
		Date: "2026-11-08", ProviderID: "prov", Model: "model", AccountID: "acct",
		Requests: 3, InputTokens: 100, OutputTokens: 200, Errors: 1,
	})
	s.flush()

	re := newUsageStore(path)
	re.load()
	re.mu.Lock()
	day := re.days["2026-11-08"]
	var found bool
	for _, c := range day {
		if c != nil && c.ProviderID == "prov" && c.Model == "model" {
			found = true
			if c.Requests != 3 || c.InputTokens != 100 || c.OutputTokens != 200 || c.Errors != 1 {
				re.mu.Unlock()
				t.Fatalf("字段丢失：%+v", c)
			}
		}
	}
	re.mu.Unlock()
	if !found {
		t.Fatal("没找到写入的记录")
	}
}

// 并发 record + flush 不能 panic（数据竞争由 -race 检验，这里先保证不崩）。
func TestConcurrentRecordAndFlush(t *testing.T) {
	s := newUsageStore(filepath.Join(t.TempDir(), "usage.json"))
	s.maxDays = 7
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				s.record(usageCell{Date: "2026-11-08", ProviderID: "p", Model: "m", AccountID: "a", Requests: 1})
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			s.flush()
		}
	}()
	wg.Wait()
}
