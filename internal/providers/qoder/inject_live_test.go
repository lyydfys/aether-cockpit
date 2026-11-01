//go:build windows

package qoder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"aether-cockpit/internal/provider"
)

// TestLiveQoderInjectRoundTrip 把本机 Qoder 当前账号原样回写一遍，
// 验证 v10 加密/DPAPI 回写可被客户端格式接受（不打印任何令牌）。
// 默认跳过；QODER_LIVE_INJECT=1 才跑。
func TestLiveQoderInjectRoundTrip(t *testing.T) {
	if os.Getenv("QODER_LIVE_INJECT") != "1" {
		t.Skip("设置 QODER_LIVE_INJECT=1 才跑真实 auth.v1.dat 回写")
	}
	var prof profile
	var ok bool
	for _, cand := range []profile{cnProfile, intlProfile} {
		if _, _, _, err := readDesktopAuthRaw(cand); err == nil {
			prof = cand
			ok = true
			break
		}
	}
	if !ok {
		t.Skip("本机没有可读的 Qoder 登录态")
	}
	_, plain, _, err := readDesktopAuthRaw(prof)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	var before map[string]any
	if err := json.Unmarshal(plain, &before); err != nil {
		t.Fatal(err)
	}
	token, _ := before["token"].(string)
	refresh, _ := before["refreshToken"].(string)
	expires, _ := before["expiresAt"].(string)
	user, _ := before["user"].(map[string]any)
	creds := map[string]string{"token": token, "refresh_token": refresh, "expires_at": expires}
	if v, _ := user["id"].(string); v != "" {
		creds["user_id"] = v
	}
	if v, _ := user["name"].(string); v != "" {
		creds["user_name"] = v
	}
	if v, _ := user["email"].(string); v != "" {
		creds["user_email"] = v
	}

	_, err = newProvider(prof).InjectLocalAccount(context.Background(), &provider.AccountEnv{
		AccountID:   "live-" + prof.id,
		ProviderID:  prof.id,
		Credentials: creds,
	})
	if err != nil {
		t.Fatalf("InjectLocalAccount: %v", err)
	}

	_, afterPlain, _, err := readDesktopAuthRaw(prof)
	if err != nil {
		t.Fatalf("回写后读取失败: %v", err)
	}
	var after map[string]any
	if err := json.Unmarshal(afterPlain, &after); err != nil {
		t.Fatal(err)
	}
	if hashOfQ(after["token"]) != hashOfQ(before["token"]) {
		t.Fatal("token 回写后不一致")
	}
	if hashOfQ(after["refreshToken"]) != hashOfQ(before["refreshToken"]) {
		t.Fatal("refreshToken 回写后不一致")
	}
	if len(after) != len(before) {
		t.Fatalf("字段数变化: %d → %d", len(before), len(after))
	}
	t.Logf("profile=%s fields=%d ok", prof.id, len(after))
}

func hashOfQ(v any) string {
	s, _ := v.(string)
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
