package gateway

import (
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

// 压缩产物绑定到签发它的账号：换号会 invalid_encrypted_content。
func TestCompactPinBindAndLookup(t *testing.T) {
	s := newCompactPinStore()
	s.Bind("SEALED-A", "acc-1")
	if got := s.Lookup("SEALED-A"); got != "acc-1" {
		t.Fatalf("应查到绑定，得到 %q", got)
	}
	// 不同密文互不影响。
	if got := s.Lookup("SEALED-B"); got != "" {
		t.Fatalf("未绑定的密文应返回空，得到 %q", got)
	}
	// 空输入不 panic、不建立绑定。
	s.Bind("", "acc-2")
	if got := s.Lookup(""); got != "" {
		t.Fatalf("空密文应返回空，得到 %q", got)
	}
}

// 绑定过期后失效，避免长期钉死在某个账号上。
func TestCompactPinExpires(t *testing.T) {
	s := newCompactPinStore()
	s.Bind("SEALED", "acc-1")
	// 手动把时间推到 TTL 之外。
	key := compactDigest("SEALED")
	s.mu.Lock()
	p := s.pins[key]
	p.at = time.Now().Add(-compactPinTTL - time.Minute)
	s.pins[key] = p
	s.mu.Unlock()
	if got := s.Lookup("SEALED"); got != "" {
		t.Fatalf("过期绑定应失效，得到 %q", got)
	}
}

// 只存指纹，不存密文原文。
func TestCompactPinStoresDigestOnly(t *testing.T) {
	s := newCompactPinStore()
	s.Bind("SECRET-BLOB", "acc-1")
	s.mu.RLock()
	defer s.mu.RUnlock()
	for k := range s.pins {
		if k == "SECRET-BLOB" {
			t.Fatal("不该以密文原文作键")
		}
		if len(k) != 64 {
			t.Fatalf("键应是 sha256 十六进制，得到 %q", k)
		}
	}
}

// 请求里带压缩载荷时，应反查出绑定账号。
func TestPinnedAccountForCompactionItem(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	g.compactPins().Bind("SEALED-X", "acc-9")

	items := []ir.UnifiedItem{
		{Type: ir.ItemTypeMessage, Role: "user", Text: "hi"},
		{Type: ir.ItemTypeCompaction, EncryptedContent: "SEALED-X"},
	}
	if got := g.pinnedAccountFor(items); got != "acc-9" {
		t.Fatalf("应反查出 acc-9，得到 %q", got)
	}
	// 没有压缩项 / 未绑定时返回空（fail-open）。
	if got := g.pinnedAccountFor([]ir.UnifiedItem{{Type: ir.ItemTypeMessage}}); got != "" {
		t.Fatalf("无压缩项应返回空，得到 %q", got)
	}
	if got := g.pinnedAccountFor([]ir.UnifiedItem{{Type: ir.ItemTypeCompaction, EncryptedContent: "UNKNOWN"}}); got != "" {
		t.Fatalf("未绑定应返回空，得到 %q", got)
	}
}

// 能从两种响应形状里取出密文。
func TestExtractSealedFromCompactResponse(t *testing.T) {
	nested := []byte(`{"object":"response.compaction","output":[{"type":"compaction","encrypted_content":"SEALED-1"}]}`)
	if got := extractSealedFromCompactResponse(nested); got != "SEALED-1" {
		t.Fatalf("嵌套形状 = %q", got)
	}
	flat := []byte(`{"encrypted_content":"SEALED-2"}`)
	if got := extractSealedFromCompactResponse(flat); got != "SEALED-2" {
		t.Fatalf("扁平形状 = %q", got)
	}
	if got := extractSealedFromCompactResponse([]byte(`{"object":"x"}`)); got != "" {
		t.Fatalf("没有密文应返回空，得到 %q", got)
	}
	if got := extractSealedFromCompactResponse([]byte("不是 JSON")); got != "" {
		t.Fatalf("非法 JSON 应返回空，得到 %q", got)
	}
}
