package pool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/providers/openaicompat"
)

// 渠道未注册的账号要能在接口上"看得见"，渠道一注册就立刻回到池里。
func TestUnloadedAccountsAreVisibleAndPromotable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	writeAccountsFile(t, path, persistedAccount{
		AccountID:  "acc-late",
		ProviderID: "custom-late-provider",
		Label:      "晚点回来的号",
	})

	m := New()
	if err := m.SetStorePath(path, nil); err != nil {
		t.Fatalf("SetStorePath: %v", err)
	}
	if got := len(m.All()); got != 0 {
		t.Fatalf("渠道未注册时不该入池: %d", got)
	}
	unloaded := m.UnloadedAccounts()
	if len(unloaded) != 1 {
		t.Fatalf("未就绪列表应有 1 条: %+v", unloaded)
	}
	if unloaded[0].AccountID != "acc-late" || unloaded[0].ProviderID != "custom-late-provider" {
		t.Fatalf("未就绪账号内容不对: %+v", unloaded[0])
	}

	// 渠道上线（自定义渠道保存 / spec reconcile 都会走到注册）。
	openaicompat.RegisterDynamic(openaicompat.Config{
		ID:      "custom-late-provider",
		BaseURL: "http://127.0.0.1:1/v1",
	})
	t.Cleanup(func() { openaicompat.UnregisterDynamic("custom-late-provider") })

	promoted, err := m.PromoteUnloaded(nil)
	if err != nil {
		t.Fatalf("PromoteUnloaded: %v", err)
	}
	if promoted != 1 {
		t.Fatalf("应提升 1 个账号, got=%d", promoted)
	}
	if _, ok := m.Get("acc-late"); !ok {
		t.Fatal("账号没有回到池里")
	}
	if got := len(m.UnloadedAccounts()); got != 0 {
		t.Fatalf("提升后不该再留在未就绪列表: %d", got)
	}

	// 提升后再保存一次：账号仍然只出现一次（不重复写）。
	if err := m.SaveAccounts(); err != nil {
		t.Fatalf("SaveAccounts: %v", err)
	}
	f := readAccountsFile(t, path)
	if len(f.Accounts) != 1 || f.Accounts[0].AccountID != "acc-late" {
		t.Fatalf("保存后账号不唯一: %+v", f.Accounts)
	}
}

// 渠道仍然没注册时，PromoteUnloaded 是空操作，账号原样留着。
func TestPromoteUnloadedKeepsUnknownProvidersUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	writeAccountsFile(t, path, persistedAccount{
		AccountID:  "acc-ghost",
		ProviderID: "custom-still-missing",
	})

	m := New()
	if err := m.SetStorePath(path, nil); err != nil {
		t.Fatalf("SetStorePath: %v", err)
	}
	promoted, err := m.PromoteUnloaded(nil)
	if err != nil {
		t.Fatalf("PromoteUnloaded: %v", err)
	}
	if promoted != 0 {
		t.Fatalf("渠道未注册时不该提升, got=%d", promoted)
	}
	if got := len(m.UnloadedAccounts()); got != 1 {
		t.Fatalf("未就绪账号应保留: %d", got)
	}
}

// readAccountsFile 读出落盘的账号文件，测试用。
func readAccountsFile(t *testing.T, path string) accountsFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取账号文件失败: %v", err)
	}
	var f accountsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("解析账号文件失败: %v", err)
	}
	return f
}

// writeAccountsFile 写一份落盘账号文件，测试用。
func writeAccountsFile(t *testing.T, path string, accounts ...persistedAccount) {
	t.Helper()
	b, err := json.Marshal(accountsFile{Version: 1, Accounts: accounts})
	if err != nil {
		t.Fatalf("序列化账号文件失败: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("写入账号文件失败: %v", err)
	}
}

// 渠道当前未注册时，账号仍然必须留在磁盘上。
//
// 这是"杀后台后接入的渠道全没了"的核心回归：加载发生在 Gateway.New 里，
// 自定义渠道 / spec 渠道那时可能还没注册；旧实现直接丢掉该账号，随后任何一次
// SaveAccounts 都会按内存池（不含它）整体回写，把它从磁盘上永久抹掉。
func TestSaveAccountsPreservesAccountsWithUnregisteredProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	writeAccountsFile(t, path, persistedAccount{
		AccountID:      "acc-ghost",
		ProviderID:     "custom-not-registered-yet",
		Label:          "用户的渠道",
		CredentialsEnc: "QUUydGVzdA==",
	})

	m := New()
	if err := m.SetStorePath(path, nil); err != nil {
		t.Fatalf("SetStorePath: %v", err)
	}
	if got := len(m.All()); got != 0 {
		t.Fatalf("未注册渠道的账号不应该进内存池, got=%d", got)
	}

	// 等价于用户之后新增/编辑了账号触发的一次落盘。
	if err := m.SaveAccounts(); err != nil {
		t.Fatalf("SaveAccounts: %v", err)
	}

	f := readAccountsFile(t, path)
	if len(f.Accounts) != 1 {
		t.Fatalf("未注册渠道的账号被写掉了: %+v", f.Accounts)
	}
	if f.Accounts[0].AccountID != "acc-ghost" || f.Accounts[0].ProviderID != "custom-not-registered-yet" {
		t.Fatalf("账号内容被改写: %+v", f.Accounts[0])
	}
	if f.Accounts[0].CredentialsEnc != "QUUydGVzdA==" {
		t.Fatalf("凭据密文被改写: %q", f.Accounts[0].CredentialsEnc)
	}
}

// 已注册渠道的账号照常进池，且未注册渠道的账号同时保留，两者互不覆盖。
func TestSaveAccountsKeepsLiveAndUnregisteredAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	writeAccountsFile(t, path,
		persistedAccount{AccountID: "acc-live", ProviderID: "openai-compat", Label: "在用的号"},
		persistedAccount{AccountID: "acc-ghost", ProviderID: "custom-not-registered-yet", Label: "待恢复的号"},
	)

	m := New()
	if err := m.SetStorePath(path, nil); err != nil {
		t.Fatalf("SetStorePath: %v", err)
	}
	if len(m.All()) != 1 {
		t.Fatalf("只有已注册渠道的账号应进池, got=%d", len(m.All()))
	}
	if _, ok := m.Get("acc-live"); !ok {
		t.Fatal("已注册渠道的账号未加载")
	}

	if err := m.SaveAccounts(); err != nil {
		t.Fatalf("SaveAccounts: %v", err)
	}

	f := readAccountsFile(t, path)
	if len(f.Accounts) != 2 {
		t.Fatalf("保存后应同时保留两个账号, got=%d: %+v", len(f.Accounts), f.Accounts)
	}
	seen := map[string]bool{}
	for _, pa := range f.Accounts {
		seen[pa.AccountID] = true
	}
	if !seen["acc-live"] || !seen["acc-ghost"] {
		t.Fatalf("账号不完整: %+v", f.Accounts)
	}
}

// 反复 SetStorePath（同一进程内多次加载）不得让保留集累加出重复账号。
func TestSaveAccountsDoesNotDuplicatePreservedAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	writeAccountsFile(t, path,
		persistedAccount{AccountID: "acc-ghost", ProviderID: "custom-not-registered-yet"},
	)

	m := New()
	for i := 0; i < 3; i++ {
		if err := m.SetStorePath(path, nil); err != nil {
			t.Fatalf("SetStorePath #%d: %v", i, err)
		}
	}
	if err := m.SaveAccounts(); err != nil {
		t.Fatalf("SaveAccounts: %v", err)
	}

	f := readAccountsFile(t, path)
	if len(f.Accounts) != 1 {
		t.Fatalf("保留的账号出现重复: %+v", f.Accounts)
	}
}

// 保留集有上限：超出时宁可不保存，也不能把放不下的账号写没。
func TestSaveAccountsRefusesWriteWhenPreservedOverflows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	accounts := make([]persistedAccount, 0, maxPreservedAccounts+1)
	for i := 0; i <= maxPreservedAccounts; i++ {
		accounts = append(accounts, persistedAccount{
			AccountID:  fmt.Sprintf("acc-%d", i),
			ProviderID: "custom-not-registered-yet",
		})
	}
	writeAccountsFile(t, path, accounts...)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取原始文件失败: %v", err)
	}

	m := New()
	if err := m.SetStorePath(path, nil); err != nil {
		t.Fatalf("SetStorePath: %v", err)
	}
	if err := m.SaveAccounts(); err == nil {
		t.Fatal("超出保留上限时 SaveAccounts 必须拒绝写盘")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取文件失败: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("拒绝写盘后账号文件不应被改动")
	}
}

// 渠道恢复注册后，账号应当正常回到内存池（证明"保留"不是死数据）。
func TestPreservedAccountLoadsOnceProviderIsRegistered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	writeAccountsFile(t, path, persistedAccount{
		AccountID:  "acc-late",
		ProviderID: "openai-compat",
		Label:      "回归的号",
	})

	m := New()
	if err := m.SetStorePath(path, nil); err != nil {
		t.Fatalf("SetStorePath: %v", err)
	}
	env, ok := m.Get("acc-late")
	if !ok {
		t.Fatal("已注册渠道的账号没有加载")
	}
	if env.ProviderID != "openai-compat" {
		t.Fatalf("provider 不对: %s", env.ProviderID)
	}
	if env.Label != "回归的号" {
		t.Fatalf("label 未恢复: %q", env.Label)
	}
}
