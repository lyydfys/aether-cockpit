package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// TestInjectAccountGuards 覆盖一键切号的鉴权/能力边界：
// 账号不存在 -> 404；渠道未实现 LocalInjectCapability -> 409。
// 成功路径需要真实渠道实现，由各渠道自己的 inject 单测覆盖。
func TestInjectAccountGuards(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	env := provider.NewAccountEnv("acc-1", "mock-native")
	g.Pool().Add(env)

	call := func(id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+id+"/inject", strings.NewReader("{}"))
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		g.handleInjectAccount(rec, req)
		return rec
	}

	if rec := call("missing"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing account status = %d", rec.Code)
	}
	if rec := call("acc-1"); rec.Code != http.StatusConflict {
		t.Fatalf("不支持注入的渠道应 409，实际 %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestInjectShouldRefresh(t *testing.T) {
	base := func(creds map[string]string) *provider.AccountEnv {
		e := provider.NewAccountEnv("a", "antigravity")
		e.Credentials = creds
		return e
	}
	if injectShouldRefresh(base(map[string]string{
		"refresh_token": "1//r",
		"expires_at":    time.Now().Add(time.Hour).Format(time.RFC3339),
	})) {
		t.Fatal("还有 1 小时有效时不应提前续期")
	}
	if !injectShouldRefresh(base(map[string]string{
		"refresh_token": "1//r",
		"expires_at":    time.Now().Add(-time.Minute).Format(time.RFC3339),
	})) {
		t.Fatal("已过期的 token 写入前应续期")
	}
	if !injectShouldRefresh(base(map[string]string{
		"refresh_token": "1//r",
		"expires_at":    time.Now().Add(time.Minute).Format(time.RFC3339Nano),
	})) {
		t.Fatal("2 分钟内到期应续期")
	}
	if injectShouldRefresh(base(map[string]string{"expires_at": time.Now().Add(-time.Hour).Format(time.RFC3339)})) {
		t.Fatal("没有 refresh_token 时不该尝试续期")
	}
	if injectShouldRefresh(base(map[string]string{"refresh_token": "1//r"})) {
		t.Fatal("没有 expires_at 时保持原样")
	}
}
