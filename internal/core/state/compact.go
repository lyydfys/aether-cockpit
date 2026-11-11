package state

import (
	"log"
	"os"
	"time"
)

// 响应快照库的容量管理。
//
// 背景：responses 表存的是"每轮对话的完整/增量历史"，用于 previous_response_id
// 续接。这类数据天然会不断累积——一个长期使用的网关，库文件涨到几百 MB 很常见
// （实测某实例 650 MB）。而原先的 pruneSQLite 没有任何生产调用方，过期行
// 永远不会被删除，库只增不减。
//
// 这里提供三件事：
//  1. Stats —— 看清当前占用与行数；
//  2. Compact —— 删过期行 + 按上限裁最旧行 + VACUUM 回收磁盘；
//  3. StartAutoCompact —— 定期自动做（默认 6 小时），并把库大小压在软上限内。

// CompactOptions 控制一次清理的力度。
type CompactOptions struct {
	// KeepRecords 最多保留多少条快照（<=0 表示按 store 的 maxRecords）。
	KeepRecords int
	// Vacuum 是否在删除后回收磁盘空间。
	//
	// VACUUM 会重建整个库文件，期间持排他锁——大库上可能耗时数秒到数十秒。
	// 因此默认只在"确实删掉了不少东西"时才做，避免每次轮询都卡一下。
	Vacuum bool
}

// CompactResult 是一次清理的结果，供接口回显。
type CompactResult struct {
	DeletedExpired int   `json:"deleted_expired"`
	DeletedOverCap int   `json:"deleted_over_cap"`
	Kept           int   `json:"kept"`
	SizeBefore     int64 `json:"size_before"`
	SizeAfter      int64 `json:"size_after"`
	Vacuumed       bool  `json:"vacuumed"`
	DurationMS     int64 `json:"duration_ms"`
}

// Stats 描述快照库当前状态。
type Stats struct {
	Backend     string `json:"backend"`
	Path        string `json:"path,omitempty"`
	Records     int    `json:"records"`
	SizeBytes   int64  `json:"size_bytes"`
	MaxRecords  int    `json:"max_records"`
	TTLHours    int    `json:"ttl_hours"`
	WALBytes    int64  `json:"wal_bytes"`
	PageCount   int64  `json:"page_count,omitempty"`
	FreePages   int64  `json:"free_pages,omitempty"`
	Reclaimable int64  `json:"reclaimable_bytes,omitempty"`
}

// Stats 返回快照库当前占用。
func (s *Store) Stats() Stats {
	st := Stats{Backend: s.Backend()}
	s.mu.Lock()
	st.Records = len(s.records)
	st.MaxRecords = s.maxRecords
	st.TTLHours = int(s.ttl / time.Hour)
	db := s.db
	path := s.dbPath
	s.mu.Unlock()

	if db == nil || path == "" {
		if path != "" {
			if fi, err := os.Stat(path); err == nil {
				st.SizeBytes = fi.Size()
			}
		}
		return st
	}
	st.Path = path
	if fi, err := os.Stat(path); err == nil {
		st.SizeBytes = fi.Size()
	}
	if fi, err := os.Stat(path + "-wal"); err == nil {
		st.WALBytes = fi.Size()
	}
	// page_count / freelist_count 能算出"删行之后还能回收多少"。
	var pageCount, freePages, pageSize int64
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err == nil {
		st.PageCount = pageCount
	}
	if err := db.QueryRow(`PRAGMA freelist_count`).Scan(&freePages); err == nil {
		st.FreePages = freePages
	}
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err == nil && pageSize > 0 {
		st.Reclaimable = freePages * pageSize
	}
	return st
}

// Compact 清理历史快照并可选回收磁盘。
//
// 顺序很关键：先删过期行（TTL 已到，本就不该被续接），再按上限裁最旧的，
// 最后才 VACUUM——反过来会把空间白折腾一遍。
func (s *Store) Compact(opt CompactOptions) (CompactResult, error) {
	start := time.Now()
	// 先把待写队列刷干净：否则清理看到的是"还没落库"的旧视图，
	// 会误报保留条数，用户也会觉得"点了清理没效果"。
	s.Flush()

	res := CompactResult{}
	res.SizeBefore = s.dbFileSize()

	s.mu.Lock()
	db := s.db
	keep := opt.KeepRecords
	if keep <= 0 {
		keep = s.maxRecords
	}
	s.mu.Unlock()

	if db == nil {
		return res, nil
	}

	s.persistMu.Lock()
	// 1) 过期行
	now := time.Now().Unix()
	if r, err := db.Exec(`DELETE FROM responses WHERE expires_at > 0 AND expires_at <= ?`, now); err == nil {
		if n, err := r.RowsAffected(); err == nil {
			res.DeletedExpired = int(n)
		}
	}
	_, _ = db.Exec(`DELETE FROM signatures WHERE expires_at > 0 AND expires_at <= ?`, now)
	_, _ = db.Exec(`DELETE FROM sessions WHERE expires_at > 0 AND expires_at <= ?`, now)

	// 2) 超出保留上限的最旧行。created_at 为 0 的历史行按最小时间处理，
	//    优先被裁掉（它们本来就无法判断新旧）。
	if keep > 0 {
		if r, err := db.Exec(`DELETE FROM responses WHERE id NOT IN (
			SELECT id FROM responses ORDER BY created_at DESC, rowid DESC LIMIT ?
		)`, keep); err == nil {
			if n, err := r.RowsAffected(); err == nil {
				res.DeletedOverCap = int(n)
			}
		}
	}

	// 3) 统计剩余行数
	_ = db.QueryRow(`SELECT COUNT(*) FROM responses`).Scan(&res.Kept)

	deleted := res.DeletedExpired + res.DeletedOverCap
	if opt.Vacuum && deleted > 0 {
		// VACUUM 必须在没有活跃事务时执行；MaxOpenConns=1 且持有 persistMu，
		// 此时不会有并发写入。
		if _, err := db.Exec(`VACUUM`); err != nil {
			log.Printf("[WARN] VACUUM 失败（不影响已删除的数据）: %v", err)
		} else {
			res.Vacuumed = true
		}
		// WAL 文件也收一下，否则磁盘占用看起来没降。
		_, _ = db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	}
	s.persistMu.Unlock()

	// 内存层同步收缩：库里删掉的行不该继续留在 LRU 里。
	if deleted > 0 {
		s.dropMissingLocked(keep)
	}

	res.SizeAfter = s.dbFileSize()
	res.DurationMS = time.Since(start).Milliseconds()
	if deleted > 0 {
		log.Printf("[STATE] 快照清理：过期 %d 条、超限 %d 条，保留 %d 条；%.1f MB → %.1f MB（%.0f ms）",
			res.DeletedExpired, res.DeletedOverCap, res.Kept,
			float64(res.SizeBefore)/(1<<20), float64(res.SizeAfter)/(1<<20),
			float64(res.DurationMS))
	}
	return res, nil
}

// dropMissingLocked 把内存 LRU 收缩到上限，与库保持一致。
func (s *Store) dropMissingLocked(keep int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.lru.Len() > keep {
		back := s.lru.Back()
		if back == nil {
			return
		}
		s.lru.Remove(back)
		delete(s.records, back.Value.(*recordEntry).key)
	}
}

func (s *Store) dbFileSize() int64 {
	s.mu.Lock()
	path := s.dbPath
	s.mu.Unlock()
	if path == "" {
		return 0
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// StartAutoCompact 定期清理快照库，返回停止函数。
//
// 默认 6 小时一次：太频繁会让 VACUUM 反复重建库文件（IO 密集），
// 太稀疏又会让库涨得很大。只有当"可回收空间"或"超限行数"确实可观时才
// 触发 VACUUM，避免每次都付出重建代价。
func (s *Store) StartAutoCompact(interval time.Duration, softLimitBytes int64) func() {
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		// 启动后先等一会儿再跑第一轮：避免和启动期的大量初始化抢 IO。
		first := time.NewTimer(2 * time.Minute)
		defer first.Stop()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-first.C:
			case <-t.C:
			}
			st := s.Stats()
			needVacuum := st.SizeBytes > softLimitBytes || st.Reclaimable > 32<<20
			if st.Records == 0 && st.SizeBytes == 0 {
				continue
			}
			_, _ = s.Compact(CompactOptions{Vacuum: needVacuum})
		}
	}()
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		close(stop)
		<-done
	}
}
