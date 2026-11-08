package state

import (
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
