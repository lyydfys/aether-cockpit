//go:build windows

package antigravity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/provider"
)

// TestLiveCredentialRoundTrip 真实回写同一账号的本机凭据，确认 CredWriteW
// 写出的 blob 官方客户端可读、字段不丢。默认跳过；AG_LIVE_INJECT=1 才跑。
//
// 安全性：写入内容来自当前本机同一账号的登录态（等价于原样重写），
// 回退文件落在临时 HOME，不碰真实 ~/.gemini。
func TestLiveCredentialRoundTrip(t *testing.T) {
	if os.Getenv("AG_LIVE_INJECT") != "1" {
		t.Skip("设置 AG_LIVE_INJECT=1 才跑真实凭据管理器回写")
	}
	raw, _, err := readLocalCredentialMeta()
	if err != nil {
		t.Fatalf("读取本机 Antigravity 凭据失败（需已登录官方客户端）: %v", err)
	}
	draft := parseLocalCredential(raw)
	if draft == nil {
		t.Fatal("本机凭据无法解析为账号")
	}

	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	env := &provider.AccountEnv{
		AccountID:   "live-check",
		ProviderID:  "antigravity",
		Credentials: draft.Credentials,
	}
	res, err := (&Provider{}).InjectLocalAccount(context.Background(), env)
	if err != nil {
		t.Fatalf("InjectLocalAccount: %v", err)
	}

	back, _, err := readLocalCredentialMeta()
	if err != nil {
		t.Fatalf("回写后 CredRead 失败: %v", err)
	}
	backDraft := parseLocalCredential(back)
	if backDraft == nil {
		t.Fatal("回写后的凭据无法解析")
	}
	if hashToken(backDraft.Credentials["access_token"]) != hashToken(draft.Credentials["access_token"]) {
		t.Fatal("回写后 access_token 与本机原值不一致")
	}
	if draft.Credentials["id_token"] != "" && backDraft.Credentials["id_token"] != draft.Credentials["id_token"] {
		t.Fatal("id_token 在回写中丢失")
	}
	if draft.Credentials["refresh_token"] != "" && backDraft.Credentials["refresh_token"] != draft.Credentials["refresh_token"] {
		t.Fatal("refresh_token 在回写中丢失")
	}

	file := filepath.Join(home, ".gemini", "jetski-standalone-oauth-token")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("回退文件未写出: %v", err)
	}
	if d := parseLocalCredential(data); d == nil {
		t.Fatal("回退文件无法解析")
	}

	t.Logf("target=%s files=%v warnings=%v", res.Target, res.Files, res.Warnings)
}

func hashToken(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
