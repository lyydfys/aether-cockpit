package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

// 断开同步时**不能**删用户自己手写的同名 MCP 服务器。
//
// 场景：用户在 .claude.json 里自己写了一个叫 "local-tools" 的 server，
// 我们在库里也有同名条目但从未对他下发过。点"全部移除"时，按名字删就会
// 把用户的那条一起删掉——这是真正会丢配置的事故。
func TestRemoveSkipsEntriesWeNeverWrote(t *testing.T) {
	g, home, _ := agentApplyTestGateway(t)

	// 用户自己的 MCP 配置（我们没有下发过，也没写过记账）。
	cfg := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(cfg, []byte(`{"mcpServers":{"user-own":{"command":"npx","args":["x"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := g.removeAgentFrom("servers", "user-own", "claude_code", map[string]bool{})
	if err == nil {
		t.Fatal("没有下发记录时应拒绝删除")
	}
	if err != errNotOurs {
		t.Fatalf("应返回 errNotOurs，得到 %v", err)
	}

	// 文件必须原样保留。
	raw, readErr := os.ReadFile(cfg)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !hasSub(string(raw), "user-own") {
		t.Fatalf("用户自己的条目被删掉了：%s", raw)
	}
}

// 下发过的条目可以正常移除，并且移除后记账也清掉。
func TestRemoveDeletesEntriesWeWrote(t *testing.T) {
	g, home, _ := agentApplyTestGateway(t)
	g.library.NoteApplied("servers", "ours", "claude_code", "hash-1")

	cfg := filepath.Join(home, ".claude.json")
	if err := os.WriteFile(cfg, []byte(`{"mcpServers":{"ours":{"command":"npx"},"theirs":{"command":"y"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := g.removeAgentFrom("servers", "ours", "claude_code", map[string]bool{}); err != nil {
		t.Fatalf("下发过的条目应能删除：%v", err)
	}
	raw, _ := os.ReadFile(cfg)
	if hasSub(string(raw), `"ours"`) {
		t.Fatalf("我方条目应被删除：%s", raw)
	}
	if !hasSub(string(raw), "theirs") {
		t.Fatalf("别人的条目不能被殃及：%s", raw)
	}
	if _, ok := g.library.AppliedFor("servers", "ours", "claude_code"); ok {
		t.Fatal("移除后记账应清掉")
	}
}

// hasSub 是测试内的简单子串判断（避免与其它测试文件里的同名 helper 冲突）。
func hasSub(s, sub string) bool { return len(sub) == 0 || indexOfSub(s, sub) >= 0 }

func indexOfSub(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
