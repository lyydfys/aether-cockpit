// Package state 是状态缝合层（StateSuture）的持久化基底。
//
// 它解决的是逆向渠道独有的问题：上游是有状态的（要先建 session 才能对话），
// 而下游客户端（OpenAI Chat / Claude Code）以为自己是无状态的。
// 网关必须在中间替两边记住"聊到哪了"，并且保证：
//  1. previous_response_id 能瞬间定位到原账号与原会话（100% 精准粘性路由）；
//  2. 原账号挂掉时，能用保存的全量快照无缝切到另一个账号；
//  3. 思考签名（thoughtSignature / signature）能按 call_id 原样回缝。
package state

import (
	"container/list"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
)

// ResponseRecord 是一次响应的完整上下文快照。
type ResponseRecord struct {
	ResponseID        string `json:"response_id"`
	PreviousID        string `json:"previous_id,omitempty"`
	ProviderID        string `json:"provider_id"`
	AccountID         string `json:"account_id"`
	UpstreamSessionID string `json:"upstream_session_id,omitempty"`
	Model             string `json:"model"`
	// AccumulatedItems 是截止本轮结束的全量上下文，用于跨账号故障转移。
	AccumulatedItems []ir.UnifiedItem `json:"accumulated_items"`
	// Output 是这次响应返回给客户端的 output 项（Responses 协议），
	// 供 GET /v1/responses/{id} 回放；Fast-Path 直通时直接取自上游 JSON。
	Output    []any `json:"output,omitempty"`
	CreatedAt int64 `json:"created_at"`
	// ExpiresAt Unix 秒，0 表示不主动过期（仍受 LRU 约束）。
	ExpiresAt int64 `json:"expires_at,omitempty"`
	// Internal 表示这条快照只用于网关内部的会话缝合，不对外提供
	// GET /v1/responses/{id} 回放。
	//
	// 存在的理由：客户端的 "store": false 语义是"**上游**不要保留这条 response"
	// （Codex 在 WebSocket 上每帧都这么发）。网关为了拼接 previous_response_id
	// 必须自己记住上下文，这两件事必须解耦——否则 store:false 会让网关也不记账，
	// 下一帧查不到就直接静默丢历史。
	Internal bool `json:"internal,omitempty"`
}

type recordEntry struct {
	key string
	rec *ResponseRecord
}

// Store 是基于内存 LRU 的状态存储，支持可选落盘。
//
// 选择内存而非 SQLite 的理由（自用场景）：
//   - 单进程、单用户，无需跨进程共享；
//   - 对话状态天然是短生命周期的，落盘价值低；
//   - 落盘快照只用于"重启后仍能续上正在进行的会话"这一种情况。
type Store struct {
	mu sync.Mutex
	// records 按 response_id 索引。
	records map[string]*list.Element
	lru     *list.List
	// sigs 按 call_id 存思考签名。
	sigs map[string]sigEntry
	// sessionByKey 把稳定的会话指纹映射到上游 session id。
	sessionByKey map[string]sessionEntry

	maxRecords int
	ttl        time.Duration
	snapshot   string
	dirty      bool

	// db 为 SQLite 连接；非空时所有写入同步落库。
	db *sql.DB
	// persistMu 单独保护数据库写入，与内存锁分离，避免磁盘 IO 阻塞读请求。
	persistMu sync.Mutex
}

type sigEntry struct {
	Signature string
	ExpiresAt time.Time
}

type sessionEntry struct {
	ProviderID string
	AccountID  string
	SessionID  string
	ExpiresAt  time.Time
}

// Options 存储配置。
type Options struct {
	// MaxRecords 最多保留多少条响应快照。
	MaxRecords int
	// TTL 快照存活时间，默认 2 小时（远大于一次编程会话的间隔）。
	TTL time.Duration
	// SnapshotPath 若非空，则定期把状态写入该 JSON 文件（SQLite 不可用时的降级方案）。
	SnapshotPath string
	// SQLitePath 若非空，则使用 SQLite 持久化。优先于 SnapshotPath。
	SQLitePath string
}

// New 创建存储。
func New(opts Options) *Store {
	if opts.MaxRecords <= 0 {
		opts.MaxRecords = 2000
	}
	if opts.TTL <= 0 {
		opts.TTL = 2 * time.Hour
	}
	s := &Store{
		records:      make(map[string]*list.Element, opts.MaxRecords),
		lru:          list.New(),
		sigs:         make(map[string]sigEntry, 512),
		sessionByKey: make(map[string]sessionEntry, 512),
		maxRecords:   opts.MaxRecords,
		ttl:          opts.TTL,
		snapshot:     opts.SnapshotPath,
	}

	// 优先 SQLite：它能做增量写入，不像 JSON 快照需要全量序列化。
	if opts.SQLitePath != "" {
		if err := s.attachSQLite(opts.SQLitePath); err != nil {
			// 打不开就降级到 JSON/内存，不能让网关因为一个持久化问题起不来。
			log.Printf("[WARN] SQLite 初始化失败，降级为 %s: %v", fallbackName(opts.SnapshotPath), err)
			s.load()
		}
		return s
	}

	s.load()
	return s
}

func fallbackName(snapshot string) string {
	if snapshot != "" {
		return "JSON 快照"
	}
	return "纯内存"
}

// PutResponse 写入或更新一条响应快照。
func (s *Store) PutResponse(rec *ResponseRecord) {
	if rec == nil || rec.ResponseID == "" {
		return
	}
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().Unix()
	}
	if rec.ExpiresAt == 0 {
		rec.ExpiresAt = time.Now().Add(s.ttl).Unix()
	}

	s.mu.Lock()
	if el, ok := s.records[rec.ResponseID]; ok {
		el.Value.(*recordEntry).rec = rec
		s.lru.MoveToFront(el)
		s.dirty = true
		s.mu.Unlock()
		s.persistResponse(rec)
		return
	}

	el := s.lru.PushFront(&recordEntry{key: rec.ResponseID, rec: rec})
	s.records[rec.ResponseID] = el
	s.dirty = true
	s.evictLocked()
	s.mu.Unlock()

	// 落盘放在锁外：磁盘 IO 不应阻塞其他请求的读取。
	s.persistResponse(rec)
}

// GetResponse 读取一条响应快照。
func (s *Store) GetResponse(id string) (*ResponseRecord, bool) {
	if id == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	el, ok := s.records[id]
	if !ok {
		return nil, false
	}
	ent := el.Value.(*recordEntry)
	if ent.rec.ExpiresAt > 0 && time.Now().Unix() > ent.rec.ExpiresAt {
		s.lru.Remove(el)
		delete(s.records, id)
		return nil, false
	}
	s.lru.MoveToFront(el)
	return ent.rec, true
}

// DeleteResponse 删除一条响应快照（对应 DELETE /v1/responses/{id}）。
func (s *Store) DeleteResponse(id string) bool {
	s.mu.Lock()
	el, ok := s.records[id]
	if !ok {
		s.mu.Unlock()
		return false
	}
	s.lru.Remove(el)
	delete(s.records, id)
	s.dirty = true
	s.mu.Unlock()

	s.deletePersistedResponse(id)
	return true
}

// Count 返回当前快照数量。
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// PutSignature 记录一次思考签名。
func (s *Store) PutSignature(callID, sig string) {
	if callID == "" || sig == "" {
		return
	}
	exp := time.Now().Add(6 * time.Hour)

	s.mu.Lock()
	s.sigs[callID] = sigEntry{Signature: sig, ExpiresAt: exp}
	s.dirty = true
	s.mu.Unlock()

	s.persistSignature(callID, sig, exp)
}

// GetSignature 读取思考签名。
func (s *Store) GetSignature(callID string) (string, bool) {
	if callID == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sigs[callID]
	if !ok {
		return "", false
	}
	if time.Now().After(e.ExpiresAt) {
		delete(s.sigs, callID)
		return "", false
	}
	return e.Signature, true
}

// PutSession 记录会话指纹到上游 session 的映射。
func (s *Store) PutSession(key, providerID, accountID, sessionID string) {
	if key == "" || sessionID == "" {
		return
	}
	exp := time.Now().Add(30 * time.Minute)

	s.mu.Lock()
	s.sessionByKey[key] = sessionEntry{
		ProviderID: providerID,
		AccountID:  accountID,
		SessionID:  sessionID,
		ExpiresAt:  exp,
	}
	s.dirty = true
	s.mu.Unlock()

	s.persistSession(key, providerID, accountID, sessionID, exp)
}

// GetSession 读取会话映射。
func (s *Store) GetSession(key string) (providerID, accountID, sessionID string, ok bool) {
	if key == "" {
		return "", "", "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, hit := s.sessionByKey[key]
	if !hit {
		return "", "", "", false
	}
	if time.Now().After(e.ExpiresAt) {
		delete(s.sessionByKey, key)
		return "", "", "", false
	}
	return e.ProviderID, e.AccountID, e.SessionID, true
}

// evictLocked 按 LRU + TTL 淘汰。
func (s *Store) evictLocked() {
	now := time.Now().Unix()
	for el := s.lru.Back(); el != nil; {
		prev := el.Prev()
		rec := el.Value.(*recordEntry).rec
		if len(s.records) <= s.maxRecords && (rec.ExpiresAt == 0 || now <= rec.ExpiresAt) {
			break
		}
		if len(s.records) <= s.maxRecords && rec.ExpiresAt > 0 && now > rec.ExpiresAt {
			// 继续向前清理过期项
			s.lru.Remove(el)
			delete(s.records, rec.ResponseID)
			el = prev
			continue
		}
		if len(s.records) > s.maxRecords {
			s.lru.Remove(el)
			delete(s.records, rec.ResponseID)
			el = prev
			continue
		}
		break
	}

	// 顺带清理过期签名与会话（数量小，全扫可接受）。
	for k, v := range s.sigs {
		if now > v.ExpiresAt.Unix() {
			delete(s.sigs, k)
		}
	}
	for k, v := range s.sessionByKey {
		if now > v.ExpiresAt.Unix() {
			delete(s.sessionByKey, k)
		}
	}
}

// --- 落盘 ---

type snapshotFile struct {
	Version    int               `json:"version"`
	SavedAt    int64             `json:"saved_at"`
	Records    []*ResponseRecord `json:"records"`
	Signatures map[string]string `json:"signatures,omitempty"`
}

// Save 把当前状态写入快照文件（若已配置路径）。
func (s *Store) Save() error {
	if s.snapshot == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}

	snap := snapshotFile{
		Version:    1,
		SavedAt:    time.Now().Unix(),
		Records:    make([]*ResponseRecord, 0, len(s.records)),
		Signatures: make(map[string]string, len(s.sigs)),
	}
	for el := s.lru.Front(); el != nil; el = el.Next() {
		snap.Records = append(snap.Records, el.Value.(*recordEntry).rec)
	}
	for k, v := range s.sigs {
		snap.Signatures[k] = v.Signature
	}

	if err := os.MkdirAll(filepath.Dir(s.snapshot), 0o755); err != nil {
		return err
	}
	tmp := s.snapshot + ".tmp"
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.snapshot); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// load 从快照文件恢复。
func (s *Store) load() {
	if s.snapshot == "" {
		return
	}
	b, err := os.ReadFile(s.snapshot)
	if err != nil {
		return
	}
	var snap snapshotFile
	if err := json.Unmarshal(b, &snap); err != nil {
		return
	}
	now := time.Now().Unix()
	// Records 按文件顺序（新→旧）推入，保持 LRU 顺序一致。
	for i := len(snap.Records) - 1; i >= 0; i-- {
		rec := snap.Records[i]
		if rec == nil || rec.ResponseID == "" {
			continue
		}
		if rec.ExpiresAt > 0 && now > rec.ExpiresAt {
			continue
		}
		el := s.lru.PushFront(&recordEntry{key: rec.ResponseID, rec: rec})
		s.records[rec.ResponseID] = el
	}
	for k, v := range snap.Signatures {
		s.sigs[k] = sigEntry{Signature: v, ExpiresAt: time.Now().Add(6 * time.Hour)}
	}
	s.dirty = false
}

// StartAutoSave 启动周期性落盘，返回停止函数（停止时会做最后一次保存并等它写完）。
func (s *Store) StartAutoSave(interval time.Duration) func() {
	if s.snapshot == "" || interval <= 0 {
		return func() {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				_ = s.Save()
				return
			case <-t.C:
				_ = s.Save()
			}
		}
	}()
	return func() {
		once.Do(func() { close(stop) })
		<-done
	}
}
