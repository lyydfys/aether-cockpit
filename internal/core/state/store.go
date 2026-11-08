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
	"sort"
	"sync"
	"sync/atomic"
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
	//
	// 注意：这个字段在内存里始终是**完整历史**（调用方依赖它直接回填）。
	// 但落盘时只写本轮增量（见 deltaItems），加载后再按 PreviousID 链拼装，
	// 避免同一段对话在每一轮都被完整复制一遍（O(n²) 存储）。
	AccumulatedItems []ir.UnifiedItem `json:"accumulated_items"`
	// deltaItems 是"本轮新增"的条目，仅用于落盘。
	// 为空表示这条记录没有前驱，AccumulatedItems 本身就是增量。
	deltaItems []ir.UnifiedItem
	// storedFull 表示"这一条落盘时写的是完整历史"（而非增量）。
	//
	// deltaOf 在前缀不匹配时返回 nil，落盘退回写全量；加载时必须知道
	// 这件事，否则会再拼一次前驱历史，让上下文翻倍。用显式标记比
	// "猜前缀"可靠。
	storedFull bool
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

	// writeCh 是"写后置"队列：落盘由后台 goroutine 串行消费。
	//
	// 为什么不直接同步写：persistResponse 虽在内存锁之外，但仍然发生在
	// 请求的收尾路径上——慢盘（机械盘/杀软实时扫描）会把每个请求的尾延迟
	// 拉高几十毫秒。队列满时回退为同步写，保证任何情况下都不丢数据。
	writeCh chan *ResponseRecord
	writeWg sync.WaitGroup
	// closed 标记队列已关闭，后续写入退化为同步。
	closed atomicBool
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
	// 有 SQLite 时启动后台落盘；没有就什么都不做（纯内存模式）。
	s.startWriter()
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
	// 先算好"本轮增量"：落盘只写它，避免每轮重复存整段历史。
	// 必须在拿 s.mu 之前调用（deltaOf 内部会加锁）。
	if rec != nil && rec.deltaItems == nil && !rec.storedFull {
		rec.deltaItems = s.deltaOf(rec)
		if rec.deltaItems == nil {
			// 算不出增量（无前驱 / 前缀不匹配 / 前驱已淘汰）：
			// 落盘写全量，并标记出来供加载时判断。
			rec.storedFull = true
		}
	}
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
		s.enqueuePersist(rec)
		return
	}

	el := s.lru.PushFront(&recordEntry{key: rec.ResponseID, rec: rec})
	s.records[rec.ResponseID] = el
	s.dirty = true
	s.evictLocked()
	s.mu.Unlock()

	// 落盘放在锁外：磁盘 IO 不应阻塞其他请求的读取。
	s.enqueuePersist(rec)
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

// atomicBool 是极简的原子布尔（只用于"队列是否已关闭"这一处）。
type atomicBool struct {
	v int32
}

func (b *atomicBool) Store(x bool) {
	var n int32
	if x {
		n = 1
	}
	atomic.StoreInt32(&b.v, n)
}

func (b *atomicBool) Load() bool { return atomic.LoadInt32(&b.v) == 1 }

// enqueuePersist 把落盘任务交给后台 goroutine，调用方立即返回。
//
// 队列满（后台写不过来，通常是慢盘）时回退为同步写：
// 宁可这一个请求慢一点，也不能丢快照——丢了就等于把用户的会话上下文弄断。
func (s *Store) enqueuePersist(rec *ResponseRecord) {
	if s.db == nil {
		return
	}
	if s.closed.Load() || s.writeCh == nil {
		s.persistResponse(rec)
		return
	}
	select {
	case s.writeCh <- rec:
	default:
		s.persistResponse(rec)
	}
}

// startWriter 启动后台落盘 goroutine（有 SQLite 时才启动）。
func (s *Store) startWriter() {
	if s.db == nil {
		return
	}
	s.writeCh = make(chan *ResponseRecord, 256)
	s.writeWg.Add(1)
	go func() {
		defer s.writeWg.Done()
		for rec := range s.writeCh {
			s.persistResponse(rec)
		}
	}()
}

// drainWriter 停止后台写入并等积压写完（关停/测试收尾用）。
func (s *Store) drainWriter() {
	if s.writeCh == nil {
		return
	}
	s.closed.Store(true)
	close(s.writeCh)
	s.writeCh = nil
	s.writeWg.Wait()
}

// assembleChainsLocked 把"增量"记录按 PreviousID 链还原成完整历史。
//
// 落盘只存增量（避免 O(n²) 存储），但调用方（回填 previous_response_id、
// GET /v1/responses、input_items）都需要完整历史。这里在加载完成后统一拼装：
// 先按链深度排序，深度小的先拼，保证每条只拼一次。
func assembleChainsLocked(records map[string]*list.Element) {
	// 收集待拼装的记录（deltaItems 非空且 delta != 完整历史时无法判断，
	// 统一按"有 PreviousID 就拼"处理；没有前驱的记录 delta 就是全量）。
	type item struct {
		rec *ResponseRecord
	}
	all := make([]*ResponseRecord, 0, len(records))
	for _, el := range records {
		all = append(all, el.Value.(*recordEntry).rec)
	}

	// 计算每条记录的链深度（沿 PreviousID 向上数，最多 maxChainDepth 层防环）。
	const maxChainDepth = 500
	depth := make(map[string]int, len(all))
	var depthOf func(id string, seen map[string]bool) int
	depthOf = func(id string, seen map[string]bool) int {
		if d, ok := depth[id]; ok {
			return d
		}
		if seen[id] || len(seen) > maxChainDepth {
			return 0
		}
		seen[id] = true
		el, ok := records[id]
		if !ok {
			return 0
		}
		prev := el.Value.(*recordEntry).rec.PreviousID
		if prev == "" {
			depth[id] = 0
			return 0
		}
		d := depthOf(prev, seen) + 1
		depth[id] = d
		return d
	}
	for _, r := range all {
		depthOf(r.ResponseID, map[string]bool{})
	}
	sort.Slice(all, func(i, j int) bool {
		return depth[all[i].ResponseID] < depth[all[j].ResponseID]
	})

	resolved := make(map[string]bool, len(all))
	for _, r := range all {
		if r.deltaItems == nil {
			resolved[r.ResponseID] = true
			continue
		}
		if r.PreviousID == "" {
			// 没有前驱：库里的就是全量。
			resolved[r.ResponseID] = true
			continue
		}
		// 有前驱：先判断库里存的到底是"增量"还是"全量"。
		//
		// deltaOf 在前缀不匹配时会返回 nil，落盘因此退回写全量。
		// 这类记录加载后不能再去拼前驱，否则历史会被重复一遍。
		// 判据：已是前驱历史的前缀 → 库里存的是全量，跳过。
		prevEl, ok := records[r.PreviousID]
		if !ok {
			// 前驱已被淘汰：保留原样，至少不会比"什么都没有"更差。
			resolved[r.ResponseID] = true
			continue
		}
		prev := prevEl.Value.(*recordEntry).rec
		if !resolved[prev.ResponseID] {
			// 前驱还没拼装（链上有断点或成环）：跳过，保持原样。
			resolved[r.ResponseID] = true
			continue
		}
		if r.storedFull || hasItemPrefix(r.deltaItems, prev.AccumulatedItems) {
			// 库里已经是完整历史（回退写全量的情况），不要再拼一次。
			resolved[r.ResponseID] = true
			continue
		}
		merged := make([]ir.UnifiedItem, 0, len(prev.AccumulatedItems)+len(r.deltaItems))
		merged = append(merged, prev.AccumulatedItems...)
		merged = append(merged, r.deltaItems...)
		r.AccumulatedItems = merged
		resolved[r.ResponseID] = true
	}
}

// deltaOf 计算"本轮新增"：完整历史减去前驱历史的前缀。
//
// 若完整历史不是以前驱历史为前缀（例如客户端改写了历史），返回 nil，
// 此时落盘退回写全量——正确性优先于存储优化。
func (s *Store) deltaOf(rec *ResponseRecord) []ir.UnifiedItem {
	if rec.PreviousID == "" || len(rec.AccumulatedItems) == 0 {
		return nil
	}
	s.mu.Lock()
	prevEl, ok := s.records[rec.PreviousID]
	var prevLen int
	var prevItems []ir.UnifiedItem
	if ok {
		prev := prevEl.Value.(*recordEntry).rec
		prevLen = len(prev.AccumulatedItems)
		prevItems = prev.AccumulatedItems
	}
	s.mu.Unlock()
	if !ok || prevLen == 0 || len(rec.AccumulatedItems) < prevLen {
		return nil
	}
	// 前缀必须完全一致才敢只存增量。
	for i := 0; i < prevLen; i++ {
		if !sameItem(prevItems[i], rec.AccumulatedItems[i]) {
			return nil
		}
	}
	return rec.AccumulatedItems[prevLen:]
}

// sameItem 比较两个 item 是否等价（只比影响回填语义的字段）。
func sameItem(a, b ir.UnifiedItem) bool {
	return a.Type == b.Type && a.Role == b.Role && a.ID == b.ID &&
		a.Text == b.Text && a.CallID == b.CallID &&
		a.Name == b.Name && a.Arguments == b.Arguments && a.Output == b.Output
}

// hasItemPrefix 判断 items 是否以 prefix 开头（用于识别"库里存的是全量"）。
func hasItemPrefix(items, prefix []ir.UnifiedItem) bool {
	if len(prefix) == 0 || len(items) < len(prefix) {
		return false
	}
	for i := range prefix {
		if !sameItem(items[i], prefix[i]) {
			return false
		}
	}
	return true
}

// boolToInt 把布尔转成 SQLite 的 0/1。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
