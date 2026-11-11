package state

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

// 清理必须真的让库文件变小，而不只是删行。
//
// 背景：pruneSQLite 原先没有任何生产调用方，过期行永不删除；
// 即便删了行，SQLite 也不会自动缩小文件（空间留在 freelist 里），
// 所以必须显式 VACUUM 才能真正回收磁盘。
func TestCompactReclaimsDiskSpace(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s := New(Options{SQLitePath: dbPath, MaxRecords: 10000, TTL: time.Hour})
	defer s.Close()

	// 写入一批已经过期的记录（模拟历史累积）。
	past := time.Now().Add(-2 * time.Hour).Unix()
	for i := 0; i < 200; i++ {
		s.PutResponse(&ResponseRecord{
			ResponseID: fmt.Sprintf("old-%d", i),
			Model:      "m",
			CreatedAt:  past,
			ExpiresAt:  past, // 已过期
			AccumulatedItems: []ir.UnifiedItem{
				{Type: ir.ItemTypeMessage, Role: "user", Text: longText(2000)},
			},
		})
	}
	// 再写几条仍然有效的，确认不会被误删。
	for i := 0; i < 5; i++ {
		s.PutResponse(&ResponseRecord{
			ResponseID: fmt.Sprintf("live-%d", i),
			Model:      "m",
			AccumulatedItems: []ir.UnifiedItem{
				{Type: ir.ItemTypeMessage, Role: "user", Text: "keep me"},
			},
		})
	}

	s.Flush()
	before := s.Stats()
	if before.SizeBytes == 0 {
		t.Fatal("库文件应已创建")
	}

	res, err := s.Compact(CompactOptions{KeepRecords: 10000, Vacuum: true})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.DeletedExpired != 200 {
		t.Fatalf("应删除 200 条过期记录，实际 %d", res.DeletedExpired)
	}
	if !res.Vacuumed {
		t.Fatal("有删除时应执行 VACUUM 回收空间")
	}
	// 用 Compact 自己测的删前大小做基准：before 里那 200 条过期记录
	// 加上删除后的 VACUUM，回收量应该非常可观。
	if res.SizeAfter >= res.SizeBefore {
		t.Fatalf("VACUUM 后文件应变小：%d -> %d", res.SizeBefore, res.SizeAfter)
	}
	t.Logf("清理：%.1f KB → %.1f KB，保留 %d 条",
		float64(res.SizeBefore)/1024, float64(res.SizeAfter)/1024, res.Kept)

	// 有效记录必须还在，且能读回完整内容。
	for i := 0; i < 5; i++ {
		rec, ok := s.GetResponse(fmt.Sprintf("live-%d", i))
		if !ok {
			t.Fatalf("live-%d 被误删", i)
		}
		if len(rec.AccumulatedItems) != 1 || rec.AccumulatedItems[0].Text != "keep me" {
			t.Fatalf("live-%d 内容不对: %+v", i, rec.AccumulatedItems)
		}
	}
}

// 超出保留上限时应裁掉最旧的，保留最新的。
func TestCompactEnforcesRecordCap(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s := New(Options{SQLitePath: dbPath, MaxRecords: 10000, TTL: 24 * time.Hour})
	defer s.Close()

	now := time.Now()
	for i := 0; i < 30; i++ {
		s.PutResponse(&ResponseRecord{
			ResponseID: fmt.Sprintf("r-%02d", i),
			Model:      "m",
			CreatedAt:  now.Add(time.Duration(i) * time.Second).Unix(),
			AccumulatedItems: []ir.UnifiedItem{
				{Type: ir.ItemTypeMessage, Role: "user", Text: "x"},
			},
		})
	}

	res, err := s.Compact(CompactOptions{KeepRecords: 10, Vacuum: true})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if res.DeletedOverCap != 20 {
		t.Fatalf("应裁掉 20 条最旧记录，实际 %d", res.DeletedOverCap)
	}
	if res.Kept != 10 {
		t.Fatalf("应保留 10 条，实际 %d", res.Kept)
	}
	// 最好那批必须还在。
	for _, id := range []string{"r-29", "r-28", "r-20"} {
		if _, ok := s.GetResponse(id); !ok {
			t.Fatalf("%s 应被保留", id)
		}
	}
	// 最旧的必须没了。
	if _, ok := s.GetResponse("r-00"); ok {
		t.Fatal("r-00 应被裁掉")
	}
}

// Stats 要如实反映库大小与记录数。
func TestStatsReportsSize(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s := New(Options{SQLitePath: dbPath, MaxRecords: 500, TTL: 2 * time.Hour})
	defer s.Close()

	if got := s.Stats(); got.Backend != "sqlite" {
		t.Fatalf("Backend = %q", got.Backend)
	}
	s.PutResponse(&ResponseRecord{
		ResponseID: "x", Model: "m",
		AccumulatedItems: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: longText(500)}},
	})
	st := s.Stats()
	if st.Records != 1 {
		t.Fatalf("Records = %d", st.Records)
	}
	if st.SizeBytes <= 0 {
		t.Fatal("SizeBytes 应为正数")
	}
	if st.MaxRecords != 500 {
		t.Fatalf("MaxRecords = %d", st.MaxRecords)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("库文件应存在: %v", err)
	}
}

// SQLite 模式必须启动写后置队列（否则请求收尾会被磁盘 IO 阻塞）。
func TestSQLiteModeStartsWriteBehindQueue(t *testing.T) {
	s := New(Options{SQLitePath: filepath.Join(t.TempDir(), "state.db"), MaxRecords: 100, TTL: time.Hour})
	defer s.Close()
	if s.writeCh == nil {
		t.Fatal("SQLite 模式下 writeCh 不应为 nil（写后置队列未启动）")
	}
}

// SQLite 已是生效后端时，不能再周期性写 JSON 快照。
//
// 两个路径是同时配置的（state.json 是 SQLite 不可用时的降级方案）。
// 旧逻辑只判断 snapshot 非空，于是 SQLite 模式下仍每 20 秒把全量快照
// 序列化写一遍——实测 state.json 涨到 226MB，与 state.db 完全重复。
func TestSQLiteModeSkipsJSONSnapshot(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.db")
	snapPath := filepath.Join(dir, "state.json")

	s := New(Options{SQLitePath: dbPath, SnapshotPath: snapPath, TTL: time.Hour})
	defer s.Close()

	s.PutResponse(&ResponseRecord{
		ResponseID:       "r1",
		CreatedAt:        time.Now().Unix(),
		ExpiresAt:        time.Now().Add(time.Hour).Unix(),
		AccumulatedItems: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"}},
	})
	s.Flush()

	// StartAutoSave 在 SQLite 模式下应立即返回空停止函数，且不产生 JSON。
	stop := s.StartAutoSave(10 * time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	stop()

	if _, err := os.Stat(snapPath); err == nil {
		t.Fatal("SQLite 模式下不应写出 state.json 快照（与 state.db 重复）")
	}
}
