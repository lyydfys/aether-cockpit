package pool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 亲和表要能落盘并在"重启"后恢复：否则每次重启所有会话都重新选号，
// 上游 prompt cache 全部作废（用户按全价重算）。
func TestStickyPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	accountsPath := filepath.Join(dir, "accounts.json")

	m := New()
	acc := provider.NewAccountEnv("acc-1", "mock-native")
	m.Add(acc)
	m.persist.path = accountsPath

	m.BindSticky("sess-abc", "acc-1")
	m.saveSticky() // BindSticky 有节流，这里强制落一次

	// 模拟重启：新 Manager 从同一路径加载。
	m2 := New()
	m2.Add(provider.NewAccountEnv("acc-1", "mock-native"))
	m2.persist.path = accountsPath
	if err := m2.LoadSticky(); err != nil {
		t.Fatalf("LoadSticky: %v", err)
	}
	got, ok := m2.StickyAccount("sess-abc")
	if !ok || got != "acc-1" {
		t.Fatalf("重启后应恢复绑定，得到 %q ok=%v", got, ok)
	}
}

// 过期条目在加载时丢弃，不占用表空间。
func TestStickyDropsExpiredOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sticky.json")
	list := []stickyFileEntry{
		{SessionKey: "live", AccountID: "acc-1", ExpiresAt: time.Now().Add(time.Hour).Unix(), BoundAt: time.Now().Unix()},
		{SessionKey: "dead", AccountID: "acc-1", ExpiresAt: time.Now().Add(-time.Hour).Unix(), BoundAt: time.Now().Add(-2 * time.Hour).Unix()},
	}
	raw, _ := json.Marshal(list)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	m := New()
	m.Add(provider.NewAccountEnv("acc-1", "mock-native"))
	m.persist.path = filepath.Join(dir, "accounts.json")
	if err := m.LoadSticky(); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.StickyAccount("live"); !ok {
		t.Fatal("未过期条目应恢复")
	}
	if _, ok := m.StickyAccount("dead"); ok {
		t.Fatal("过期条目应被丢弃")
	}
}

// 账号已经不存在时（删号/换机）跳过绑定，避免绑到空气上。
func TestStickySkipsUnknownAccount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sticky.json")
	list := []stickyFileEntry{
		{SessionKey: "s1", AccountID: "ghost", ExpiresAt: time.Now().Add(time.Hour).Unix(), BoundAt: time.Now().Unix()},
	}
	raw, _ := json.Marshal(list)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	m := New() // 不给它加任何账号
	m.persist.path = filepath.Join(dir, "accounts.json")
	if err := m.LoadSticky(); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.StickyAccount("s1"); ok {
		t.Fatal("账号不存在时不该恢复绑定")
	}
}

// 损坏的亲和文件不该让网关起不来（它只是优化项）。
func TestStickyCorruptFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sticky.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New()
	m.persist.path = filepath.Join(dir, "accounts.json")
	if err := m.LoadSticky(); err != nil {
		t.Fatalf("损坏文件应被忽略而不是报错：%v", err)
	}
}

// 落盘条数有上限：超出时保留最近的，文件不会无限增长。
func TestStickyCapsEntries(t *testing.T) {
	dir := t.TempDir()
	m := New()
	m.Add(provider.NewAccountEnv("acc-1", "mock-native"))
	m.persist.path = filepath.Join(dir, "accounts.json")

	now := time.Now()
	m.mu.Lock()
	for i := 0; i < stickyPersistMax+50; i++ {
		m.sticky[string(rune('a'+i%26))+string(rune('0'+i%10))+itoaSmall(i)] = stickyBind{
			accountID: "acc-1",
			boundAt:   now.Add(-time.Duration(i) * time.Second),
			expiresAt: now.Add(time.Hour),
		}
	}
	m.mu.Unlock()

	m.saveSticky()
	raw, err := os.ReadFile(filepath.Join(dir, "sticky.json"))
	if err != nil {
		t.Fatal(err)
	}
	var list []stickyFileEntry
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) > stickyPersistMax {
		t.Fatalf("落盘条数应 <= %d，得到 %d", stickyPersistMax, len(list))
	}
}

func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
