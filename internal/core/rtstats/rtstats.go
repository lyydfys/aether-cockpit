// Package rtstats 统计上游响应 id 的"后端族群"分布，用来看官转率。
//
// 为什么需要它：OpenCode 这类渠道背后是"原生官方透传 + 翻译层 + 自部署 cell"
// 的动态混合池，2026-11 的 v2 之后上游会把非法参数夹掉再转发，客户端再也
// 无法用行为探针（stop 超长、top_logprobs 越界…）判断"这一跳是不是官方"。
// 唯一还活着的信号是响应 id 的前缀族：
//
//	chatcmpl-… / 纯数字    官方后端（推定）
//	gw-…                  网关/cell（自部署或翻译层，推定）
//	其它                   未知
//
// 所以这里统计的是**推定口径**，不是上游承认的 SLA。界面上必须如实标注，
// 否则会变成"看起来很准其实靠猜"的数字。
package rtstats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Family 是响应 id 归到的后端族群。
type Family string

const (
	FamilyOfficial Family = "official"
	FamilyRelay    Family = "relay"
	FamilyUnknown  Family = "unknown"
)

// Counters 是某渠道的累计计数。
type Counters struct {
	Total    int64 `json:"total"`
	Official int64 `json:"official"`
	Relay    int64 `json:"relay"`
	Unknown  int64 `json:"unknown"`
	// UpdatedAt 是最后一次记录的时间（unix 秒）。
	UpdatedAt int64 `json:"updated_at,omitempty"`
}

// Share 返回官方占比（0..1）；没有样本时返回 0。
func (c Counters) Share() float64 {
	if c.Total == 0 {
		return 0
	}
	return float64(c.Official) / float64(c.Total)
}

// classifyID 把一个响应 id 归到族群。纯函数，便于单测。
func classifyID(id string) Family {
	v := strings.ToLower(strings.TrimSpace(id))
	if v == "" {
		return FamilyUnknown
	}
	switch {
	case strings.HasPrefix(v, "gw-"):
		return FamilyRelay
	case strings.Contains(v, "cell"):
		return FamilyRelay
	case strings.HasPrefix(v, "chatcmpl-"):
		return FamilyOfficial
	case strings.HasPrefix(v, "msg_"): // Anthropic 原生形态
		return FamilyOfficial
	case strings.HasPrefix(v, "resp_"): // OpenAI Responses 原生形态
		return FamilyOfficial
	}
	// 纯数字 id（例如 021793…）：实测出现在官方路径上。
	numeric := true
	for _, r := range v {
		if r < '0' || r > '9' {
			numeric = false
			break
		}
	}
	if numeric {
		return FamilyOfficial
	}
	return FamilyUnknown
}

// Store 是进程内的统计表；按渠道累加，落盘尽力而为。
type Store struct {
	mu   sync.Mutex
	data map[string]*Counters
	path string
	// lastFlush 用于节流，避免每个响应都写盘。
	lastFlush time.Time
}

var global = &Store{data: map[string]*Counters{}}

// SetPath 设置落盘路径并尝试加载已有统计。
//
// 统计是"锦上添花"：文件不存在/损坏都不该影响网关启动，读失败就当空表。
func SetPath(path string) {
	global.mu.Lock()
	global.path = path
	global.mu.Unlock()
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var loaded map[string]*Counters
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return
	}
	global.mu.Lock()
	for k, v := range loaded {
		if v != nil {
			global.data[k] = v
		}
	}
	global.mu.Unlock()
}

// Record 记录一次响应 id。
func Record(providerID, id string) {
	global.record(providerID, id)
}

func (s *Store) record(providerID, id string) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return
	}
	fam := classifyID(id)
	s.mu.Lock()
	c := s.data[providerID]
	if c == nil {
		c = &Counters{}
		s.data[providerID] = c
	}
	c.Total++
	switch fam {
	case FamilyOfficial:
		c.Official++
	case FamilyRelay:
		c.Relay++
	default:
		c.Unknown++
	}
	c.UpdatedAt = time.Now().Unix()
	shouldFlush := s.path != "" && time.Since(s.lastFlush) > 3*time.Second
	if shouldFlush {
		s.lastFlush = time.Now()
	}
	snapshot := s.snapshotLocked()
	path := s.path
	s.mu.Unlock()
	if shouldFlush {
		writeFile(path, snapshot)
	}
}

// Snapshot 返回某渠道的计数副本。
func Snapshot(providerID string) Counters {
	global.mu.Lock()
	defer global.mu.Unlock()
	if c := global.data[providerID]; c != nil {
		return *c
	}
	return Counters{}
}

// All 返回全部渠道的计数副本（按渠道 id 排序，保证输出稳定）。
func All() map[string]Counters {
	global.mu.Lock()
	defer global.mu.Unlock()
	return global.snapshotLocked()
}

func (s *Store) snapshotLocked() map[string]Counters {
	out := make(map[string]Counters, len(s.data))
	for k, v := range s.data {
		out[k] = *v
	}
	return out
}

// SortedProviders 返回有统计的渠道 id（稳定顺序），供 UI 直接遍历。
func SortedProviders() []string {
	all := All()
	ids := make([]string, 0, len(all))
	for k := range all {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return ids
}

// writeFile 原子写：先写 .tmp 再 rename，避免读到半个文件。
func writeFile(path string, data map[string]Counters) {
	if path == "" {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// Flush 立即落盘（退出/手动刷新时用）。
func Flush() {
	global.mu.Lock()
	snap := global.snapshotLocked()
	path := global.path
	global.mu.Unlock()
	writeFile(path, snap)
}
