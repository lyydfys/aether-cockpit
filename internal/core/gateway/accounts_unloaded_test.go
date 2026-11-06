package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/providers/openaicompat"
)

// 概览「可用账号」的数据口径：account_total / ready_total / unloaded_accounts。
type accountTotals struct {
	AccountTotal int `json:"account_total"`
	ReadyTotal   int `json:"ready_total"`
	Unloaded     []struct {
		AccountID  string `json:"account_id"`
		ProviderID string `json:"provider_id"`
		Label      string `json:"label"`
	} `json:"unloaded_accounts"`
}

func readAccountTotals(t *testing.T, g *Gateway) accountTotals {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/providers 状态码 = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body accountTotals
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析 /api/providers 失败: %v", err)
	}
	return body
}

// uniqueChannelID 给每个测试一个独立的渠道 id。
//
// 为什么必须唯一：provider 注册表是包级全局状态，前一个测试注册过的自定义渠道
// 会残留到后一个测试里（"渠道未注册"的场景就构造不出来了）。
func uniqueChannelID(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, r := range strings.ToLower(t.Name()) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	id := strings.Trim(b.String(), "-")
	if len(id) > 32 {
		id = id[:32]
	}
	// 注册过的动态渠道在测试结束后下线，别污染同包其它用例。
	t.Cleanup(func() { openaicompat.UnregisterDynamic("custom-" + id) })
	return id
}

// seedAccountDir 造一个「1 个自定义渠道账号」的数据目录。
// withChannel=false 时不写 settings.json 里的 custom_channels，
// 用来模拟「渠道条目丢失/尚未注册，但账号文件还在」。
func seedAccountDir(t *testing.T, channelID string, withChannel bool) (settingsPath, accountsPath string) {
	t.Helper()
	dir := t.TempDir()
	settingsPath = filepath.Join(dir, "settings.json")
	accountsPath = filepath.Join(dir, "accounts.json")

	settings := `{"privacy_mask":{"enabled":false},"price_currency":"USD"}`
	if withChannel {
		settings = `{"privacy_mask":{"enabled":false},"price_currency":"USD",` +
			`"custom_channels":[{"id":"` + channelID + `","name":"` + channelID + `",` +
			`"base_url":"http://127.0.0.1:19090/v1","protocol":"openai"}]}`
	}
	if err := os.WriteFile(settingsPath, []byte(settings), 0o600); err != nil {
		t.Fatalf("写 settings.json 失败: %v", err)
	}

	// 账号归属的渠道 id 是 custom-<id>（customChannelProviderID）。
	accounts := `{"version":1,"saved_at":1793832914,"accounts":[{"account_id":"` +
		`custom-` + channelID + `-1","provider_id":"custom-` + channelID + `","label":"我的渠道",` +
		`"network_policy":"system","weight":1,"created_at":1793832914}]}`
	if err := os.WriteFile(accountsPath, []byte(accounts), 0o600); err != nil {
		t.Fatalf("写 accounts.json 失败: %v", err)
	}
	return settingsPath, accountsPath
}

func newAccountTestGateway(t *testing.T, settingsPath, accountsPath string) *Gateway {
	t.Helper()
	g := New(Config{
		Addr:             "127.0.0.1:0",
		SettingsPath:     settingsPath,
		AccountStorePath: accountsPath,
		UsagePath:        "-",
	})
	t.Cleanup(func() { _ = g.Close(context.Background()) })
	return g
}

// 正常情况：渠道注册在账号加载之前完成，账号进池，概览数得到。
func TestProvidersCountLoadedAccount(t *testing.T) {
	id := uniqueChannelID(t)
	settingsPath, accountsPath := seedAccountDir(t, id, true)
	g := newAccountTestGateway(t, settingsPath, accountsPath)

	if got := len(g.Pool().All()); got != 1 {
		t.Fatalf("账号未进池: %d", got)
	}
	totals := readAccountTotals(t, g)
	if totals.AccountTotal != 1 {
		t.Fatalf("account_total = %d, 期望 1", totals.AccountTotal)
	}
	if totals.ReadyTotal != 1 {
		t.Fatalf("ready_total = %d, 期望 1", totals.ReadyTotal)
	}
	if len(totals.Unloaded) != 0 {
		t.Fatalf("不应有未就绪账号: %+v", totals.Unloaded)
	}
}

// 渠道未注册时：账号不能从界面上消失，要作为「未就绪」可见并计入总数。
func TestProvidersCountUnloadedAccountWhenChannelMissing(t *testing.T) {
	id := uniqueChannelID(t)
	settingsPath, accountsPath := seedAccountDir(t, id, false)
	g := newAccountTestGateway(t, settingsPath, accountsPath)

	if got := len(g.Pool().All()); got != 0 {
		t.Fatalf("渠道未注册时不该入池: %d", got)
	}
	totals := readAccountTotals(t, g)
	// 磁盘上有 1 个账号：总数为 1、可用为 0、未就绪列表里有它。
	if totals.AccountTotal != 1 {
		t.Fatalf("account_total = %d, 期望 1（磁盘真实账号数）", totals.AccountTotal)
	}
	if totals.ReadyTotal != 0 {
		t.Fatalf("ready_total = %d, 期望 0", totals.ReadyTotal)
	}
	if len(totals.Unloaded) != 1 {
		t.Fatalf("未就绪列表应有 1 条: %+v", totals.Unloaded)
	}
	wantProvider := "custom-" + id
	if totals.Unloaded[0].ProviderID != wantProvider {
		t.Fatalf("未就绪账号的渠道 = %q, 期望 %q", totals.Unloaded[0].ProviderID, wantProvider)
	}
	if totals.Unloaded[0].AccountID != wantProvider+"-1" {
		t.Fatalf("未就绪账号 id = %q", totals.Unloaded[0].AccountID)
	}
}

// 关键回归：渠道未注册 → 重启 → 账号仍在（不被清零、也不被回写抹掉）。
func TestUnloadedAccountSurvivesRestart(t *testing.T) {
	id := uniqueChannelID(t)
	settingsPath, accountsPath := seedAccountDir(t, id, false)

	first := newAccountTestGateway(t, settingsPath, accountsPath)
	// 模拟一次账号变更触发的落盘：未注册渠道的账号必须被原样保留。
	if err := first.Pool().SaveAccounts(); err != nil {
		t.Fatalf("SaveAccounts: %v", err)
	}
	_ = first.Close(context.Background())

	second := newAccountTestGateway(t, settingsPath, accountsPath)
	totals := readAccountTotals(t, second)
	if totals.AccountTotal != 1 {
		t.Fatalf("重启后 account_total = %d, 期望 1（账号被清零了）", totals.AccountTotal)
	}
	if len(totals.Unloaded) != 1 {
		t.Fatalf("重启后未就绪列表应有 1 条: %+v", totals.Unloaded)
	}
	raw, err := os.ReadFile(accountsPath)
	if err != nil {
		t.Fatalf("读 accounts.json 失败: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("accounts.json 被清空")
	}
	if !json.Valid(raw) {
		t.Fatal("accounts.json 不是合法 JSON")
	}
}

// 渠道后来注册了：未就绪账号要立刻回到池里，不用重启。
func TestUnloadedAccountPromotedWhenChannelAppears(t *testing.T) {
	id := uniqueChannelID(t)
	settingsPath, accountsPath := seedAccountDir(t, id, false)
	g := newAccountTestGateway(t, settingsPath, accountsPath)

	if got := len(g.Pool().All()); got != 0 {
		t.Fatalf("渠道未注册时不该入池: %d", got)
	}

	// 用户在运行期把渠道补回来（POST /api/custom-channels 走的就是 syncCustomChannels）。
	g.settingsMu.Lock()
	g.settings.CustomChannels = append(g.settings.CustomChannels, CustomChannel{
		ID: id, Name: id, BaseURL: "http://127.0.0.1:19090/v1", Protocol: "openai",
	})
	g.settingsMu.Unlock()
	g.syncCustomChannels()

	if got := len(g.Pool().All()); got != 1 {
		t.Fatalf("渠道注册后账号应回到池里, got=%d", got)
	}
	totals := readAccountTotals(t, g)
	if totals.AccountTotal != 1 || totals.ReadyTotal != 1 || len(totals.Unloaded) != 0 {
		t.Fatalf("提升后口径不对: total=%d ready=%d unloaded=%d",
			totals.AccountTotal, totals.ReadyTotal, len(totals.Unloaded))
	}
}
