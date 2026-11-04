package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

// 窗口配额闸必须能通过 API 设置：这个能力（"用到窗口多少就按住"）之前
// 只有落盘字段、没有任何入口，用户只能手改 accounts.json。
func TestAccountWindowCapsAPI(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	env := provider.NewAccountEnv("acc-1", "mock-native")
	g.Pool().Add(env)

	post := func(payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/accounts/acc-1/settings", strings.NewReader(payload))
		req.SetPathValue("id", "acc-1")
		out := httptest.NewRecorder()
		g.handleAccountSettings(out, req)
		return out
	}

	rec := post(`{"window_caps":{"all":90,"windows":{"5H":60," 7d ":80}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	caps := env.WindowCaps()
	if caps.All != 90 {
		t.Fatalf("all = %d", caps.All)
	}
	// 窗口键要归一（去空白、小写），否则配置里多写一个空格就失效。
	if got := caps.Of("5h"); got != 60 {
		t.Fatalf("5h = %d，期望 60", got)
	}
	if got := caps.Of("7d"); got != 80 {
		t.Fatalf("7d = %d，期望 80", got)
	}
	// 没单独配置的窗口回落到 all。
	if got := caps.Of("30d"); got != 90 {
		t.Fatalf("未配置窗口应回落到 all=90，得到 %d", got)
	}

	// 越界值要被收敛：负数 → 0（不限制），>100 → 100（不限制）。
	rec = post(`{"window_caps":{"all":-5,"windows":{"5h":250}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	caps = env.WindowCaps()
	if caps.All != 0 {
		t.Fatalf("负数应收敛成 0，得到 %d", caps.All)
	}
	if caps.Of("5h") != 100 {
		t.Fatalf(">100 应收敛成 100，得到 %d", caps.Of("5h"))
	}
	if caps.Holds() {
		t.Fatal("全部不限制时 Holds 应为 false")
	}

	// 不传 window_caps 时保持原状（部分更新语义）。
	rec = post(`{"priority":3}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := env.WindowCaps().Of("5h"); got != 100 {
		t.Fatalf("未提交的字段应保持不变，得到 %d", got)
	}
}

// 设置窗口 cap 后要落盘，重启仍然生效。
func TestAccountWindowCapsPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: path,
	})
	env := provider.NewAccountEnv("acc-1", "mock-native")
	env.Credentials = map[string]string{"api_key": "sk-1"}
	g.Pool().Add(env)
	env.SetWindowCaps(provider.WindowCaps{All: 80})
	g.saveAccountsNow()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取账号文件失败：%v", err)
	}
	if !strings.Contains(string(raw), "window_caps") {
		t.Fatalf("账号文件里应有 window_caps：%s", string(raw))
	}
	var doc struct {
		Accounts []struct {
			WindowCaps provider.WindowCaps `json:"window_caps"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(doc.Accounts) != 1 || doc.Accounts[0].WindowCaps.All != 80 {
		t.Fatalf("落盘的 cap 不对：%+v", doc.Accounts)
	}
}
