package pool

// 会话亲和的落盘。
//
// 为什么需要落盘：亲和（sticky）决定"同一个会话继续用哪个账号"，它直接
// 关系到上游 prompt cache 能不能命中——命中就便宜、不命中就按全价重算。
// 但亲和此前是纯内存态：网关一重启（升级、崩溃、用户关开）所有会话都回到
// 随机选号，长会话的缓存全部作废。aether 会把亲和写到磁盘（512 条 / 24h），
// 这里对齐同样的语义。
//
// 三个刻意的边界：
//   - 只存 accountID + 过期时间 + 缓存量，不存任何请求内容；
//   - 过期条目在加载时丢弃，文件不会无限长；
//   - 落盘是"尽力而为"：写失败只影响下次重启后的命中率，绝不影响本次请求。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	// stickyPersistMax 是落盘的最大条数。超出时按"最近绑定"保留，
	// 太久没动静的会话本来也过了 TTL，没有保存价值。
	stickyPersistMax = 512
	// stickyPersistTTL 是落盘条目的最长寿命（24 小时），与 aether 一致。
	stickyPersistTTL = 24 * time.Hour
)

// stickyFileEntry 是落盘的一条亲和记录。
type stickyFileEntry struct {
	SessionKey  string `json:"session"`
	AccountID   string `json:"account"`
	ExpiresAt   int64  `json:"expires_at"`
	BoundAt     int64  `json:"bound_at"`
	CacheTokens int    `json:"cache_tokens,omitempty"`
}

// stickyPath 返回亲和文件路径（与 accounts.json 同目录）。
func (m *Manager) stickyPath() string {
	m.persist.mu.Lock()
	defer m.persist.mu.Unlock()
	if m.persist.path == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(m.persist.path), "sticky.json")
}

// LoadSticky 从磁盘恢复亲和绑定（启动时调用一次）。
func (m *Manager) LoadSticky() error {
	path := m.stickyPath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []stickyFileEntry
	if err := json.Unmarshal(raw, &list); err != nil {
		// 文件坏了就当没有：亲和是"优化项"，不能因为它让网关起不来。
		return nil
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range list {
		if e.SessionKey == "" || e.AccountID == "" {
			continue
		}
		exp := time.Unix(e.ExpiresAt, 0)
		if !exp.After(now) {
			continue
		}
		if now.Sub(time.Unix(e.BoundAt, 0)) > stickyPersistTTL {
			continue
		}
		// 账号已经不存在（删号/换机）时跳过，避免绑定到空气。
		if _, ok := m.index[e.AccountID]; !ok {
			continue
		}
		m.sticky[e.SessionKey] = stickyBind{
			accountID:   e.AccountID,
			expiresAt:   exp,
			boundAt:     time.Unix(e.BoundAt, 0),
			cacheTokens: e.CacheTokens,
		}
	}
	return nil
}

// saveSticky 把当前亲和表写回磁盘（原子替换）。
func (m *Manager) saveSticky() {
	path := m.stickyPath()
	if path == "" {
		return
	}
	now := time.Now()
	m.mu.RLock()
	list := make([]stickyFileEntry, 0, len(m.sticky))
	for key, b := range m.sticky {
		if !b.expiresAt.After(now) {
			continue
		}
		if now.Sub(b.boundAt) > stickyPersistTTL {
			continue
		}
		list = append(list, stickyFileEntry{
			SessionKey:  key,
			AccountID:   b.accountID,
			ExpiresAt:   b.expiresAt.Unix(),
			BoundAt:     b.boundAt.Unix(),
			CacheTokens: b.cacheTokens,
		})
	}
	m.mu.RUnlock()

	// 超出上限时保留"最近绑定的"。
	sort.Slice(list, func(i, j int) bool { return list[i].BoundAt > list[j].BoundAt })
	if len(list) > stickyPersistMax {
		list = list[:stickyPersistMax]
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// flushSticky 在绑定/续期后按节奏落盘（最多每 5 秒一次），
// 避免高频请求把磁盘写穿。
func (m *Manager) flushSticky() {
	last := m.stickySavedNs.Load()
	now := time.Now().UnixNano()
	if now-last < int64(5*time.Second) {
		return
	}
	if !m.stickySavedNs.CompareAndSwap(last, now) {
		return
	}
	m.saveSticky()
}
