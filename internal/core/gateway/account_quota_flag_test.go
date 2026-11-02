package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// TestAccountQuotaSupportedFlag 保证账号卡能区分“额度读取失败”和“渠道根本不支持额度”。
func TestAccountQuotaSupportedFlag(t *testing.T) {
	g := New(Config{
		MaxAttempts:             1,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:               "-",
		MaxRecords:              20,
	})
	g.Pool().Add(provider.NewAccountEnv("quota-yes", "mock-native"))
	g.Pool().Add(provider.NewAccountEnv("quota-no", "redact-native-test"))

	rec := httptest.NewRecorder()
	g.handleAccounts(rec, httptest.NewRequest("GET", "/api/accounts", nil))
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Accounts []struct {
			ProviderID     string `json:"provider_id"`
			QuotaSupported bool   `json:"quota_supported"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析: %v", err)
	}
	got := map[string]bool{}
	for _, a := range out.Accounts {
		got[a.ProviderID] = a.QuotaSupported
	}
	if !got["mock-native"] {
		t.Fatal("mock-native 声明了额度能力，quota_supported 应为 true")
	}
	if got["redact-native-test"] {
		t.Fatal("redact-native-test 没有额度能力，quota_supported 应为 false")
	}
}
