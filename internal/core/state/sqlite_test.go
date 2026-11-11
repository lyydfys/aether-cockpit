package state

import (
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

// TestSQLitePersistence 验证跨重启的持久化：这是 SQLite 后端存在的唯一理由。
func TestSQLitePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	s1 := New(Options{MaxRecords: 100, SQLitePath: path})
	if s1.Backend() != "sqlite" {
		t.Fatalf("后端应为 sqlite，实际 %s", s1.Backend())
	}

	s1.PutResponse(&ResponseRecord{
		ResponseID:        "resp_1",
		PreviousID:        "",
		ProviderID:        "p",
		AccountID:         "a",
		UpstreamSessionID: "sess_9",
		Model:             "m",
		AccumulatedItems: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "第一轮"},
			{Type: ir.ItemTypeMessage, Role: "assistant", Text: "回答"},
		},
	})
	s1.PutSignature("call_1", "sig_abc")
	s1.PutSession("key1", "p", "a", "sess_9")
	if err := s1.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	// 模拟重启
	s2 := New(Options{MaxRecords: 100, SQLitePath: path})
	defer s2.Close()

	rec, ok := s2.GetResponse("resp_1")
	if !ok {
		t.Fatal("重启后未恢复快照")
	}
	if len(rec.AccumulatedItems) != 2 || rec.AccumulatedItems[0].Text != "第一轮" {
		t.Errorf("上下文未恢复: %+v", rec.AccumulatedItems)
	}
	if rec.UpstreamSessionID != "sess_9" {
		t.Errorf("上游会话未恢复: %q", rec.UpstreamSessionID)
	}

	sig, ok := s2.GetSignature("call_1")
	if !ok || sig != "sig_abc" {
		t.Errorf("签名未恢复: %q %v", sig, ok)
	}

	pid, aid, sid, ok := s2.GetSession("key1")
	if !ok || pid != "p" || aid != "a" || sid != "sess_9" {
		t.Errorf("会话绑定未恢复: %q %q %q %v", pid, aid, sid, ok)
	}
}

func TestSQLiteDeletePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	s1 := New(Options{SQLitePath: path})
	s1.PutResponse(&ResponseRecord{ResponseID: "r1", AccumulatedItems: []ir.UnifiedItem{}})
	if !s1.DeleteResponse("r1") {
		t.Fatal("删除应返回 true")
	}
	s1.Close()

	s2 := New(Options{SQLitePath: path})
	defer s2.Close()
	if _, ok := s2.GetResponse("r1"); ok {
		t.Error("删除应当持久化")
	}
}

func TestSQLiteSkipsExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")

	s1 := New(Options{SQLitePath: path})
	// 直接写一条已过期的记录
	s1.PutResponse(&ResponseRecord{
		ResponseID: "expired",
		ExpiresAt:  time.Now().Add(-time.Hour).Unix(),
	})
	s1.Close()

	s2 := New(Options{SQLitePath: path})
	defer s2.Close()
	if _, ok := s2.GetResponse("expired"); ok {
		t.Error("过期记录不应被恢复")
	}
}

// TestSQLiteFallbackToMemory 数据库路径不可用时必须降级而不是 panic。
func TestSQLiteFallbackToMemory(t *testing.T) {
	// 用一个非法路径（把文件当目录）触发打开失败
	bad := filepath.Join(t.TempDir(), "notadir", "state.db")
	s := New(Options{SQLitePath: bad})
	defer s.Close()

	// 降级后依然可用
	s.PutResponse(&ResponseRecord{ResponseID: "x", AccumulatedItems: []ir.UnifiedItem{}})
	if _, ok := s.GetResponse("x"); !ok {
		t.Error("降级后内存存储应仍可用")
	}
}

func TestSQLitePrune(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := New(Options{SQLitePath: path})
	defer s.Close()

	s.PutResponse(&ResponseRecord{
		ResponseID: "old",
		ExpiresAt:  time.Now().Add(-time.Minute).Unix(),
	})
	s.PutResponse(&ResponseRecord{
		ResponseID: "fresh",
		ExpiresAt:  time.Now().Add(time.Hour).Unix(),
	})

	s.Flush() // 写后置队列：先让记录落盘，否则清理看不到它们
	s.pruneSQLite()

	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM responses`).Scan(&n)
	if n != 1 {
		t.Errorf("清理后应剩 1 条，实际 %d", n)
	}
}

func TestBackendNames(t *testing.T) {
	mem := New(Options{})
	if mem.Backend() != "memory" {
		t.Errorf("无路径时后端应为 memory，实际 %s", mem.Backend())
	}

	jsonPath := filepath.Join(t.TempDir(), "s.json")
	js := New(Options{SnapshotPath: jsonPath})
	if js.Backend() != "json" {
		t.Errorf("配了快照路径时后端应为 json，实际 %s", js.Backend())
	}
}
