package gateway

// 压缩载荷的账号绑定。
//
// 为什么需要它：OpenAI Responses 的 compaction 产物是**上游签发**的密文，
// 只有签发它的那个账号（以及它背后的上游）能解开。Codex 长会话压缩后，
// 下一次请求会带着这段密文回来；如果我们把这次请求路由到**另一个账号**，
// 上游就会报 invalid_encrypted_content —— 用户看到的是"压缩完就续不上"。
//
// 做法：压缩成功时把"密文指纹 → 账号"记下来（24 小时），后续请求里只要
// 出现对应的密文，就用 PinnedAccount 强制回到原账号。
//
// 边界：只记 sha256 指纹，不存密文本身；记录过期即失效；找不到绑定就按
// 正常路由走（fail-open，不能因为丢了一条绑定就把请求卡死）。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"aether-cockpit/internal/core/ir"
)

// compactPinTTL 是压缩绑定的有效期。取 24 小时：与亲和一致，
// 超过一天的压缩产物基本也该重新压缩了。
const compactPinTTL = 24 * time.Hour

type compactPin struct {
	accountID string
	at        time.Time
}

type compactPinStore struct {
	mu   sync.RWMutex
	pins map[string]compactPin
}

func newCompactPinStore() *compactPinStore {
	return &compactPinStore{pins: map[string]compactPin{}}
}

// digest 计算密文的指纹（不保存原文）。
func compactDigest(sealed string) string {
	if sealed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sealed))
	return hex.EncodeToString(sum[:])
}

// Bind 记录"这段压缩载荷由哪个账号签发"。
func (s *compactPinStore) Bind(sealed, accountID string) {
	if s == nil || accountID == "" {
		return
	}
	key := compactDigest(sealed)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 顺手清理过期项，避免无界增长。
	if len(s.pins) > 4096 {
		now := time.Now()
		for k, v := range s.pins {
			if now.Sub(v.at) > compactPinTTL {
				delete(s.pins, k)
			}
		}
	}
	s.pins[key] = compactPin{accountID: accountID, at: time.Now()}
}

// Lookup 返回该密文对应的账号；过期或未记录返回空串。
func (s *compactPinStore) Lookup(sealed string) string {
	if s == nil {
		return ""
	}
	key := compactDigest(sealed)
	if key == "" {
		return ""
	}
	s.mu.RLock()
	p, ok := s.pins[key]
	s.mu.RUnlock()
	if !ok || time.Since(p.at) > compactPinTTL {
		return ""
	}
	return p.accountID
}

// pinnedAccountFor 在请求里找压缩载荷，返回它绑定的账号（没有则空串）。
//
// 只认第一条带密文的压缩项：一个请求里出现多段压缩产物时，它们必然来自
// 同一条会话（同一次压缩链），取第一个即可。
func (g *Gateway) pinnedAccountFor(items []ir.UnifiedItem) string {
	for _, it := range items {
		if it.Type != ir.ItemTypeCompaction || it.EncryptedContent == "" {
			continue
		}
		if acc := g.compactPins().Lookup(it.EncryptedContent); acc != "" {
			return acc
		}
	}
	return ""
}

// compactPins 返回压缩绑定表（惰性创建）。
func (g *Gateway) compactPins() *compactPinStore {
	g.compactPinOnce.Do(func() {
		if g.compactPin == nil {
			g.compactPin = newCompactPinStore()
		}
	})
	return g.compactPin
}

// extractSealedFromCompactResponse 从压缩端点响应里取出密文。
//
// 响应的形状是 {"object":"response.compaction","output":[{"type":"compaction",
// "encrypted_content":"..."}]}；也兼容顶层直接给 encrypted_content 的分叉。
func extractSealedFromCompactResponse(raw []byte) string {
	var doc struct {
		EncryptedContent string `json:"encrypted_content"`
		Output           []struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	if doc.EncryptedContent != "" {
		return doc.EncryptedContent
	}
	for _, o := range doc.Output {
		if o.EncryptedContent != "" {
			return o.EncryptedContent
		}
	}
	return ""
}
