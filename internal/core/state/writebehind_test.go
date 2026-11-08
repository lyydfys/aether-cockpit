package state

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

// 落盘必须不阻塞请求路径。
//
// 历史问题：persistResponse 在请求收尾时同步写 SQLite，慢盘（机械盘、
// 杀软实时扫描）会把每个请求的尾延迟拉高。现在改为写后置队列。
func TestPutResponseDoesNotWaitForDisk(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s := New(Options{SQLitePath: dbPath, MaxRecords: 100, TTL: time.Hour})
	defer s.Close()

	// 预热：先写一条，确保连接已建立、语句已准备。
	s.PutResponse(&ResponseRecord{
		ResponseID: "warmup", Model: "m",
		AccumulatedItems: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "x"}},
	})

	// 连续写入 50 条，测量单次最大耗时。
	// 写后置生效时这里应该只做入队（微秒级），而不是等每次 fsync。
	var maxCost time.Duration
	for i := 0; i < 50; i++ {
		rec := &ResponseRecord{
			ResponseID: fmt.Sprintf("r%d", i),
			Model:      "m",
			AccumulatedItems: []ir.UnifiedItem{
				{Type: ir.ItemTypeMessage, Role: "user", Text: "hello"},
			},
		}
		start := time.Now()
		s.PutResponse(rec)
		if d := time.Since(start); d > maxCost {
			maxCost = d
		}
	}
	t.Logf("单次 PutResponse 最大耗时 %v", maxCost)

	// 排队后单次写入应当很快；若退化成同步写，50 次里必然出现毫秒级尖峰。
	if maxCost > 50*time.Millisecond {
		t.Fatalf("PutResponse 最大耗时 %v，说明仍在同步等盘", maxCost)
	}

	// 关停时会排空队列，数据必须全部落盘。
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 重新打开：50 条都应在（证明队列被完整排空）。
	s2 := New(Options{SQLitePath: dbPath, MaxRecords: 100, TTL: time.Hour})
	defer s2.Close()
	missing := 0
	for i := 0; i < 50; i++ {
		if _, ok := s2.GetResponse(fmt.Sprintf("r%d", i)); !ok {
			missing++
		}
	}
	if missing != 0 {
		t.Fatalf("%d 条快照没落盘（写后置丢数据）", missing)
	}
}

// 队列满时应回退为同步写，绝不能丢数据。
func TestPutResponseFallsBackWhenQueueFull(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s := New(Options{SQLitePath: dbPath, MaxRecords: 5000, TTL: time.Hour})
	defer s.Close()

	// 一次写入远超队列容量（256）的条数。
	const n = 400
	for i := 0; i < n; i++ {
		s.PutResponse(&ResponseRecord{
			ResponseID: fmt.Sprintf("q%d", i),
			Model:      "m",
			AccumulatedItems: []ir.UnifiedItem{
				{Type: ir.ItemTypeMessage, Role: "user", Text: "x"},
			},
		})
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2 := New(Options{SQLitePath: dbPath, MaxRecords: 5000, TTL: time.Hour})
	defer s2.Close()
	missing := 0
	for i := 0; i < n; i++ {
		if _, ok := s2.GetResponse(fmt.Sprintf("q%d", i)); !ok {
			missing++
		}
	}
	if missing != 0 {
		t.Fatalf("%d/%d 条丢了（队列满时回退同步写失效）", missing, n)
	}
}

// 关闭之后再写不能 panic，也不能丢（退化为同步写或静默忽略，但不应崩溃）。
func TestPutResponseAfterCloseDoesNotPanic(t *testing.T) {
	s := New(Options{SQLitePath: filepath.Join(t.TempDir(), "state.db"), MaxRecords: 10, TTL: time.Hour})
	_ = s.Close()
	// 关闭后写入：内存里仍可记录，落盘会被跳过。这里只要求不 panic。
	s.PutResponse(&ResponseRecord{ResponseID: "after", Model: "m"})
	if _, ok := s.GetResponse("after"); !ok {
		t.Fatal("关闭后写入应仍可读到（内存层）")
	}
}
