package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"aether-cockpit/internal/provider"
)

// TestLiveCursorInjectRoundTrip 把本机 Cursor 当前账号原样回写一遍，
// 验证 Blob 写入与回读一致（不打印任何令牌）。默认跳过，CURSOR_LIVE_INJECT=1 才跑。
func TestLiveCursorInjectRoundTrip(t *testing.T) {
	if os.Getenv("CURSOR_LIVE_INJECT") != "1" {
		t.Skip("设置 CURSOR_LIVE_INJECT=1 才跑真实 state.vscdb 回写")
	}
	dbPath := cursorStateDBPath()
	draft, err := readCursorAuth(dbPath)
	if err != nil {
		t.Fatalf("读取本机 Cursor 登录态失败（需已登录）: %v", err)
	}
	res, err := (&Provider{}).InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:   "live-check",
		ProviderID:  channelID,
		Credentials: draft.Credentials,
	})
	if err != nil {
		t.Fatalf("InjectLocalAccount: %v", err)
	}
	back, err := readCursorAuth(dbPath)
	if err != nil {
		t.Fatalf("回写后读取失败: %v", err)
	}
	if hashOf(back.Credentials["access_token"]) != hashOf(draft.Credentials["access_token"]) {
		t.Fatal("access_token 回写后不一致")
	}
	if draft.Credentials["refresh_token"] != "" &&
		hashOf(back.Credentials["refresh_token"]) != hashOf(draft.Credentials["refresh_token"]) {
		t.Fatal("refresh_token 回写后不一致")
	}
	if back.Credentials["email"] != draft.Credentials["email"] {
		t.Fatal("cachedEmail 回写后不一致")
	}
	t.Logf("target=%s backups=%v", res.Target, res.Files)
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
