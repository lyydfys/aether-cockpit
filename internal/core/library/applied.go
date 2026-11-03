package library

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// 下发记账（Applied）：解决"断开同步时把用户手写的同名配置一起删掉"。
//
// 问题很具体：MCP 服务器在目标配置里只是一个同名条目，子代理只是一个同名
// 文件。按名字删，就无法区分"这条是我写进去的"和"这条是用户自己在编辑器里
// 写的"。用户的同名 server 被我们的"移除"清掉，是真正会丢配置的事故。
//
// 做法（对齐 aether 的 Applied）：写入时记一条 (kind, name, agent) -> 内容摘要；
// 删除时先查这份清单，查不到就不动文件，只在结果里报"跳过"。

// AppliedEntry 是一条下发记录。
type AppliedEntry struct {
	// Kind 是 servers / skills / hooks / subagents。
	Kind string `json:"kind"`
	Name string `json:"name"`
	// Agent 是注入目标（客户端的 base id，可能是 WSL 前缀形式）。
	Agent string `json:"agent"`
	// Hash 是写入内容的摘要。文件内容被改过就不再等于它，
	// 说明用户手工编辑过——此时删除要更谨慎。
	Hash string `json:"hash,omitempty"`
	At   int64  `json:"at,omitempty"`
}

// appliedKey 是清单的键。
func appliedKey(kind, name, agent string) string {
	return strings.Join([]string{kind, name, agent}, "|")
}

// contentHash 计算写入内容的摘要（稳定、与行序无关的部分由调用方保证）。
func contentHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// HashContent 是 contentHash 的导出形式，供网关侧记账时算摘要。
func HashContent(parts ...string) string { return contentHash(parts...) }

// NoteApplied 记录一次成功下发。
func (s *Store) NoteApplied(kind, name, agent, hash string) {
	if kind == "" || name == "" || agent == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Applied == nil {
		s.Applied = map[string]AppliedEntry{}
	}
	s.Applied[appliedKey(kind, name, agent)] = AppliedEntry{
		Kind: kind, Name: name, Agent: agent, Hash: hash, At: time.Now().Unix(),
	}
	s.flushLockedAndLog()
}

// ForgetApplied 删除一条下发记录（移除成功后调用）。
func (s *Store) ForgetApplied(kind, name, agent string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Applied == nil {
		return
	}
	if _, ok := s.Applied[appliedKey(kind, name, agent)]; !ok {
		return
	}
	delete(s.Applied, appliedKey(kind, name, agent))
	s.flushLockedAndLog()
}

// AppliedFor 查一条下发记录。
func (s *Store) AppliedFor(kind, name, agent string) (AppliedEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.Applied[appliedKey(kind, name, agent)]
	return e, ok
}

// AppliedList 返回某个类别的全部下发记录（供"只移除我自己写的"遍历）。
func (s *Store) AppliedList(kind string) []AppliedEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AppliedEntry, 0, len(s.Applied))
	for _, e := range s.Applied {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// OwnedBy 判断某条目是不是我方写进去的。
//
// 返回值第二项表示"文件内容是否仍等于我们写入时的样子"：被用户改过时
// 删除会给出提示，但仍然允许删除（用户明确点了移除）。
func (s *Store) OwnedBy(kind, name, agent string) (owned bool, unchanged bool) {
	e, ok := s.AppliedFor(kind, name, agent)
	if !ok {
		return false, false
	}
	return true, e.Hash != ""
}
