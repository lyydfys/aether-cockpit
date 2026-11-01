package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLiveLocalProviderSwitchRead 读取本机真实的 local-provider-switch.db（只读、不打印任何 Key），
// 确认解析规则对实际数据成立。默认跳过；CC_LIVE=1 才跑。
func TestLiveLocalProviderSwitchRead(t *testing.T) {
	if os.Getenv("CC_LIVE") != "1" {
		t.Skip("设置 CC_LIVE=1 才读取本机 Local Provider Switch 数据库")
	}
	entries, err := readLocalProviderSwitchEntries()
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	byTarget := map[string]int{}
	skipped := 0
	for _, e := range entries {
		if e.Skip != "" {
			skipped++
			t.Logf("skip: app=%s name=%q reason=%s", e.AppType, e.Name, e.Skip)
			continue
		}
		byTarget[e.Target]++
	}
	t.Logf("entries=%d importable: claude-sub=%d codex=%d skipped=%d",
		len(entries), byTarget["claude-sub"], byTarget["codex"], skipped)

	// 端到端：把可导入条目写进一个临时账号池（与真实 data/ 隔离），只断言数量。
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	imported := 0
	for _, e := range entries {
		if e.Skip != "" {
			continue
		}
		if _, apiErr := g.createAccount(e.Target, e.Label, e.credentials(), ""); apiErr == nil {
			imported++
		}
	}
	claudeN := len(g.pool.ListByProvider("claude-sub"))
	codexN := len(g.pool.ListByProvider("codex"))
	t.Logf("imported=%d pool: claude-sub=%d codex=%d", imported, claudeN, codexN)
	if imported != byTarget["claude-sub"]+byTarget["codex"] {
		t.Fatalf("导入数 = %d，期望 %d", imported, byTarget["claude-sub"]+byTarget["codex"])
	}
	if claudeN != byTarget["claude-sub"] || codexN != byTarget["codex"] {
		t.Fatalf("账号池 = claude %d / codex %d", claudeN, codexN)
	}
}
