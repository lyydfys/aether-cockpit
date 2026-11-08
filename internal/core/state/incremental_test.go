package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

// 落盘只存增量：重启后必须还原出**完整历史**。
//
// 背景（报告第 5 项）：AccumulatedItems 每轮都保存整个 req.Items，
// 而 req.Items 已含上一轮回填的全部历史，导致存储量随轮数 O(n²) 增长。
// 现在库里只写本轮增量，加载后按 PreviousID 链拼装。
func TestIncrementalPersistRestoresFullHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s := New(Options{SQLitePath: dbPath, MaxRecords: 1000, TTL: time.Hour})

	// 造一条 10 轮的链，每轮追加 2 个 item（user + assistant）。
	var full []ir.UnifiedItem
	prevID := ""
	for turn := 0; turn < 10; turn++ {
		full = append(full,
			ir.UnifiedItem{Type: ir.ItemTypeMessage, Role: "user", Text: fmt.Sprintf("u%d", turn)},
			ir.UnifiedItem{Type: ir.ItemTypeMessage, Role: "assistant", Text: fmt.Sprintf("a%d", turn)},
		)
		id := fmt.Sprintf("resp-%d", turn)
		// 完整历史是前驱历史的超集（真实回填就是这种形状）。
		items := append([]ir.UnifiedItem(nil), full...)
		s.PutResponse(&ResponseRecord{
			ResponseID: id, PreviousID: prevID, Model: "m",
			AccumulatedItems: items,
		})
		prevID = id
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 重启：最后一条必须还原成 20 条完整历史。
	s2 := New(Options{SQLitePath: dbPath, MaxRecords: 1000, TTL: time.Hour})
	defer s2.Close()

	for turn := 0; turn < 10; turn++ {
		rec, ok := s2.GetResponse(fmt.Sprintf("resp-%d", turn))
		if !ok {
			t.Fatalf("resp-%d 丢失", turn)
		}
		want := (turn + 1) * 2
		if len(rec.AccumulatedItems) != want {
			t.Fatalf("resp-%d 历史长度 = %d，want %d（增量拼装出错）",
				turn, len(rec.AccumulatedItems), want)
		}
		// 内容顺序也要对。
		for i := 0; i < want; i++ {
			if rec.AccumulatedItems[i].Text != full[i].Text {
				t.Fatalf("resp-%d 第 %d 项 = %q，want %q",
					turn, i, rec.AccumulatedItems[i].Text, full[i].Text)
			}
		}
	}
}

// 增量落盘必须真的减小存储量（否则等于没优化）。
func TestIncrementalPersistReducesStorage(t *testing.T) {
	mkChain := func(incremental bool) int64 {
		dbPath := filepath.Join(t.TempDir(), "state.db")
		s := New(Options{SQLitePath: dbPath, MaxRecords: 1000, TTL: time.Hour})
		var full []ir.UnifiedItem
		prevID := ""
		for turn := 0; turn < 20; turn++ {
			full = append(full,
				ir.UnifiedItem{Type: ir.ItemTypeMessage, Role: "user", Text: longText(200)},
				ir.UnifiedItem{Type: ir.ItemTypeMessage, Role: "assistant", Text: longText(200)},
			)
			id := fmt.Sprintf("r-%d", turn)
			items := append([]ir.UnifiedItem(nil), full...)
			rec := &ResponseRecord{
				ResponseID: id, PreviousID: prevID, Model: "m",
				AccumulatedItems: items,
			}
			if incremental {
				s.PutResponse(rec) // 正常路径：自动只落增量
			} else {
				s.PutResponseFull(rec) // 对照组：强制落全量
			}
			prevID = id
		}
		_ = s.Close()

		// 统计库文件大小。
		info, err := osStat(dbPath)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		return info
	}

	fullSize := mkChain(false)
	incSize := mkChain(true)
	t.Logf("全量落盘 %.1f KB → 增量落盘 %.1f KB（省 %.0f%%）",
		float64(fullSize)/1024, float64(incSize)/1024,
		(1-float64(incSize)/float64(fullSize))*100)

	if incSize >= fullSize {
		t.Fatalf("增量落盘没有变小：全量=%d 增量=%d", fullSize, incSize)
	}
	// 20 轮下增量应至少省掉一半。
	if incSize > fullSize/2 {
		t.Fatalf("增量落盘节省不足：全量=%d 增量=%d", fullSize, incSize)
	}
}

// PutResponseFull 强制落全量（测试对照组用）。
func (s *Store) PutResponseFull(rec *ResponseRecord) {
	rec.deltaItems = rec.AccumulatedItems
	s.PutResponseRaw(rec)
}

// PutResponseRaw 跳过自动 delta 计算（测试用）。
func (s *Store) PutResponseRaw(rec *ResponseRecord) {
	s.mu.Lock()
	if el, ok := s.records[rec.ResponseID]; ok {
		el.Value.(*recordEntry).rec = rec
		s.lru.MoveToFront(el)
		s.dirty = true
		s.mu.Unlock()
		s.enqueuePersist(rec)
		return
	}
	el := s.lru.PushFront(&recordEntry{key: rec.ResponseID, rec: rec})
	s.records[rec.ResponseID] = el
	s.dirty = true
	s.evictLocked()
	s.mu.Unlock()
	s.enqueuePersist(rec)
}

func osStat(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// 前缀不匹配时会退回写全量；加载时不能再拼一次前驱历史（否则历史翻倍）。
func TestFullPersistFallbackDoesNotDuplicate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s := New(Options{SQLitePath: dbPath, MaxRecords: 1000, TTL: time.Hour})

	// 第 1 轮：A、B 两条。
	s.PutResponse(&ResponseRecord{
		ResponseID: "p1", Model: "m",
		AccumulatedItems: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "user", Text: "A"},
			{Type: ir.ItemTypeMessage, Role: "assistant", Text: "B"},
		},
	})
	// 第 2 轮：客户端改写了历史（不是 p1 的超集）→ deltaOf 返回 nil → 落全量。
	rewritten := []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "X"}, // 与 A 不同
		{Type: ir.ItemTypeMessage, Role: "assistant", Text: "Y"},
		{Type: ir.ItemTypeMessage, Role: "user", Text: "Z"},
	}
	s.PutResponse(&ResponseRecord{
		ResponseID: "p2", PreviousID: "p1", Model: "m",
		AccumulatedItems: append([]ir.UnifiedItem(nil), rewritten...),
	})
	_ = s.Close()

	s2 := New(Options{SQLitePath: dbPath, MaxRecords: 1000, TTL: time.Hour})
	defer s2.Close()
	rec, ok := s2.GetResponse("p2")
	if !ok {
		t.Fatal("p2 丢失")
	}
	// 必须仍然是 3 条，不能变成 2+3=5 条。
	if len(rec.AccumulatedItems) != 3 {
		t.Fatalf("历史被重复拼装：长度 = %d，want 3（%+v）",
			len(rec.AccumulatedItems), rec.AccumulatedItems)
	}
	if rec.AccumulatedItems[0].Text != "X" {
		t.Fatalf("首项 = %q，want X", rec.AccumulatedItems[0].Text)
	}
}

// 旧库升级：responses 表没有 stored_full 列时，必须能自动补列并正常读取。
func TestOldSchemaMigrationAddsStoredFull(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")

	// 手工建一个"旧版"库：只有老列，没有 stored_full。
	// 这里不能用 openSQLite（它会顺手建最新表结构并补列）。
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE responses (
		id TEXT PRIMARY KEY, previous_id TEXT, provider_id TEXT, account_id TEXT,
		upstream_session TEXT, model TEXT, items BLOB, output BLOB,
		created_at INTEGER, expires_at INTEGER)`)
	if err != nil {
		t.Fatalf("create old table: %v", err)
	}
	items := mustJSONStr(t, []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "老数据"}})
	_, err = db.Exec(`INSERT INTO responses (id, previous_id, provider_id, account_id, upstream_session, model, items, output, created_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		"old-1", "", "p", "a", "", "m", items, nil, 1, 0)
	if err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	_ = db.Close()

	// 用当前代码打开：应自动补列，且旧数据可读。
	s := New(Options{SQLitePath: dbPath, MaxRecords: 100, TTL: time.Hour})
	defer s.Close()
	rec, ok := s.GetResponse("old-1")
	if !ok {
		t.Fatal("旧数据读不出来（迁移破坏了兼容性）")
	}
	if len(rec.AccumulatedItems) != 1 || rec.AccumulatedItems[0].Text != "老数据" {
		t.Fatalf("旧数据内容不对: %+v", rec.AccumulatedItems)
	}
	// 旧记录没有前驱，不需要拼装，长度必须仍是 1。
	if rec.storedFull {
		t.Fatal("旧记录不该被标记为 storedFull")
	}
}

func mustJSONStr(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
