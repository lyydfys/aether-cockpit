package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	// 纯 Go 的 SQLite 实现，不需要 cgo —— 这是选它而不是 mattn/go-sqlite3 的原因：
	// 项目要保持"单 exe 可直接跑"，不能引入 C 工具链依赖。
	_ "modernc.org/sqlite"
)

// schema 是建表语句。
//
// 三张表对应三类需要跨重启存活的状态：
//   - responses：previous_response_id 的链式快照（客户端靠它续轮）
//   - signatures：思考签名（丢了上游会 400）
//   - sessions：会话指纹到上游 session 的绑定
const schema = `
CREATE TABLE IF NOT EXISTS responses (
    id               TEXT PRIMARY KEY,
    previous_id      TEXT,
    provider_id      TEXT,
    account_id       TEXT,
    upstream_session TEXT,
    model            TEXT,
    items            BLOB,
    output           BLOB,
    created_at       INTEGER,
    expires_at       INTEGER
);
CREATE INDEX IF NOT EXISTS idx_responses_expires ON responses(expires_at);

CREATE TABLE IF NOT EXISTS signatures (
    call_id    TEXT PRIMARY KEY,
    signature  TEXT NOT NULL,
    expires_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_signatures_expires ON signatures(expires_at);

CREATE TABLE IF NOT EXISTS sessions (
    key        TEXT PRIMARY KEY,
    provider_id TEXT,
    account_id TEXT,
    session_id TEXT,
    expires_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
`

// openSQLite 打开（并初始化）数据库。
func openSQLite(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	// WAL 模式：读写并发不互锁，且崩溃后能自动恢复。
	// busy_timeout 避免多协程写入时直接报 database is locked。
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 的写是串行的，连接数开太大反而增加锁竞争。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化表结构失败: %w", err)
	}
	// 旧库补列：重复列错误可忽略（SQLite 没有 IF NOT EXISTS 的 ADD COLUMN）。
	if _, err := db.Exec(`ALTER TABLE responses ADD COLUMN output BLOB`); err != nil &&
		!strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		log.Printf("[WARN] responses.output 迁移失败: %v", err)
	}
	return db, nil
}

// attachSQLite 挂载数据库并恢复历史数据。
func (s *Store) attachSQLite(path string) error {
	db, err := openSQLite(path)
	if err != nil {
		return err
	}
	s.db = db
	return s.restoreFromSQLite()
}

// persistResponse 把一条快照写入数据库（upsert）。
func (s *Store) persistResponse(rec *ResponseRecord) {
	if s.db == nil || rec == nil {
		return
	}
	// 只落本轮增量：有前驱时把完整历史再次写一遍会让存储量按轮数平方增长。
	// deltaItems 为空 = 没有前驱，此时 AccumulatedItems 本身就是增量。
	toStore := rec.deltaItems
	if toStore == nil {
		toStore = rec.AccumulatedItems
	}
	items, err := json.Marshal(toStore)
	if err != nil {
		return
	}
	var output []byte
	if rec.Output != nil {
		if b, err := json.Marshal(rec.Output); err == nil {
			output = b
		}
	}

	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	_, err = s.db.Exec(`
		INSERT INTO responses (id, previous_id, provider_id, account_id, upstream_session, model, items, output, created_at, expires_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			previous_id=excluded.previous_id,
			provider_id=excluded.provider_id,
			account_id=excluded.account_id,
			upstream_session=excluded.upstream_session,
			model=excluded.model,
			items=excluded.items,
			output=excluded.output,
			created_at=excluded.created_at,
			expires_at=excluded.expires_at`,
		rec.ResponseID, rec.PreviousID, rec.ProviderID, rec.AccountID,
		rec.UpstreamSessionID, rec.Model, items, output, rec.CreatedAt, rec.ExpiresAt)
	if err != nil {
		log.Printf("[WARN] 快照落盘失败 %s: %v", rec.ResponseID, err)
	}
}

// deletePersistedResponse 删除一条落盘快照。
func (s *Store) deletePersistedResponse(id string) {
	if s.db == nil {
		return
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	_, _ = s.db.Exec(`DELETE FROM responses WHERE id = ?`, id)
}

// persistSignature 落盘一个签名。
func (s *Store) persistSignature(callID, sig string, expiresAt time.Time) {
	if s.db == nil {
		return
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	_, err := s.db.Exec(`
		INSERT INTO signatures (call_id, signature, expires_at) VALUES (?,?,?)
		ON CONFLICT(call_id) DO UPDATE SET signature=excluded.signature, expires_at=excluded.expires_at`,
		callID, sig, expiresAt.Unix())
	if err != nil {
		log.Printf("[WARN] 签名落盘失败 %s: %v", callID, err)
	}
}

// persistSession 落盘一个会话绑定。
func (s *Store) persistSession(key, providerID, accountID, sessionID string, expiresAt time.Time) {
	if s.db == nil {
		return
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	_, err := s.db.Exec(`
		INSERT INTO sessions (key, provider_id, account_id, session_id, expires_at) VALUES (?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET
			provider_id=excluded.provider_id,
			account_id=excluded.account_id,
			session_id=excluded.session_id,
			expires_at=excluded.expires_at`,
		key, providerID, accountID, sessionID, expiresAt.Unix())
	if err != nil {
		log.Printf("[WARN] 会话落盘失败 %s: %v", key, err)
	}
}

// restoreFromSQLite 启动时从数据库恢复未过期的状态。
func (s *Store) restoreFromSQLite() error {
	if s.db == nil {
		return nil
	}
	now := time.Now().Unix()

	// --- 快照 ---
	rows, err := s.db.Query(`
		SELECT id, previous_id, provider_id, account_id, upstream_session, model, items, output, created_at, expires_at
		FROM responses
		WHERE expires_at = 0 OR expires_at > ?
		ORDER BY created_at ASC
		LIMIT ?`, now, s.maxRecords)
	if err != nil {
		return fmt.Errorf("读取快照失败: %w", err)
	}
	defer rows.Close()

	s.mu.Lock()
	for rows.Next() {
		var (
			rec    ResponseRecord
			items  []byte
			output []byte
		)
		if err := rows.Scan(&rec.ResponseID, &rec.PreviousID, &rec.ProviderID, &rec.AccountID,
			&rec.UpstreamSessionID, &rec.Model, &items, &output, &rec.CreatedAt, &rec.ExpiresAt); err != nil {
			continue
		}
		if len(items) > 0 {
			_ = json.Unmarshal(items, &rec.AccumulatedItems)
			// 标记为"待拼装"：如果这条有 PreviousID，说明库里存的是增量，
			// 需要在所有行读完后按链补齐完整历史。
			rec.deltaItems = rec.AccumulatedItems
		}
		if len(output) > 0 {
			_ = json.Unmarshal(output, &rec.Output)
		}
		if _, exists := s.records[rec.ResponseID]; exists {
			continue
		}
		el := s.lru.PushFront(&recordEntry{key: rec.ResponseID, rec: &rec})
		s.records[rec.ResponseID] = el
	}
	// 所有行读完后再按 PreviousID 链拼装完整历史。
	// 必须在锁内做：拼装会读取其它记录的 AccumulatedItems。
	assembleChainsLocked(s.records)
	s.mu.Unlock()

	// --- 签名 ---
	sigRows, err := s.db.Query(`SELECT call_id, signature, expires_at FROM signatures WHERE expires_at > ?`, now)
	if err == nil {
		defer sigRows.Close()
		s.mu.Lock()
		for sigRows.Next() {
			var (
				callID, sig string
				exp         int64
			)
			if err := sigRows.Scan(&callID, &sig, &exp); err == nil {
				s.sigs[callID] = sigEntry{Signature: sig, ExpiresAt: time.Unix(exp, 0)}
			}
		}
		s.mu.Unlock()
	}

	// --- 会话绑定 ---
	sessRows, err := s.db.Query(`SELECT key, provider_id, account_id, session_id, expires_at FROM sessions WHERE expires_at > ?`, now)
	if err == nil {
		defer sessRows.Close()
		s.mu.Lock()
		for sessRows.Next() {
			var (
				key, pid, aid, sid string
				exp                int64
			)
			if err := sessRows.Scan(&key, &pid, &aid, &sid, &exp); err == nil {
				s.sessionByKey[key] = sessionEntry{
					ProviderID: pid, AccountID: aid, SessionID: sid,
					ExpiresAt: time.Unix(exp, 0),
				}
			}
		}
		s.mu.Unlock()
	}

	return nil
}

// pruneSQLite 清理过期行。由后台任务定期调用。
func (s *Store) pruneSQLite() {
	if s.db == nil {
		return
	}
	now := time.Now().Unix()
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	_, _ = s.db.Exec(`DELETE FROM responses WHERE expires_at > 0 AND expires_at <= ?`, now)
	_, _ = s.db.Exec(`DELETE FROM signatures WHERE expires_at > 0 AND expires_at <= ?`, now)
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE expires_at > 0 AND expires_at <= ?`, now)
}

// Close 关闭数据库连接。
func (s *Store) Close() error {
	// 先排空后台落盘队列，再关连接：否则最后几条快照会写到已关闭的 db 上。
	s.drainWriter()

	s.mu.Lock()
	db := s.db
	s.db = nil
	s.mu.Unlock()
	if db != nil {
		return db.Close()
	}
	return nil
}

// Backend 返回当前持久化后端名称（供诊断展示）。
func (s *Store) Backend() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return "sqlite"
	}
	if s.snapshot != "" {
		return "json"
	}
	return "memory"
}
