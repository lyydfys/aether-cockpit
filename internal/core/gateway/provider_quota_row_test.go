package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 渠道编辑器账号行的额度徽标（custom-quota.js）依赖 keyList[].quota：
// 有读数的账号带 plan/windows/readAt，没有读数的账号不带该字段。
func TestProvidersKeyListCarriesQuota(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})

	with := mustCreateAccount(t, g, "mock-native", "quota@example.com")
	with.SetQuota(&provider.QuotaStatus{
		Plan:    "Pro",
		Windows: []provider.QuotaWindow{{Name: "5 hours", Used: 42, ResetsAt: time.Now().Add(3 * time.Hour).Unix()}},
		ReadAt:  time.Now().Unix(),
	})
	_ = mustCreateAccount(t, g, "mock-native", "plain@example.com")

	rec := httptest.NewRecorder()
	g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Providers []struct {
			ID      string `json:"id"`
			KeyList []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Quota *struct {
					Plan    string `json:"plan"`
					Windows []struct {
						Name     string  `json:"name"`
						Used     float64 `json:"used"`
						ResetsAt string  `json:"resetsAt"`
					} `json:"windows"`
					ReadAt int64 `json:"readAt"`
				} `json:"quota"`
			} `json:"keyList"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	var quotaRow, plainRow bool
	for _, p := range body.Providers {
		if p.ID != "mock-native" {
			continue
		}
		for _, k := range p.KeyList {
			switch k.Name {
			case "quota@example.com":
				quotaRow = true
				if k.Quota == nil {
					t.Fatal("有读数的账号缺少 quota 字段")
				}
				if k.Quota.Plan != "Pro" || len(k.Quota.Windows) != 1 || k.Quota.Windows[0].Used != 42 {
					t.Fatalf("quota 形状不对: %+v", k.Quota)
				}
				if !strings.HasPrefix(k.Quota.Windows[0].ResetsAt, "20") {
					t.Errorf("resetsAt 应为 ISO: %q", k.Quota.Windows[0].ResetsAt)
				}
				if k.Quota.ReadAt <= 0 {
					t.Errorf("readAt 应为毫秒时间戳: %d", k.Quota.ReadAt)
				}
			case "plain@example.com":
				plainRow = true
				if k.Quota != nil {
					t.Errorf("无读数的账号不应带 quota: %+v", k.Quota)
				}
			}
		}
	}
	if !quotaRow || !plainRow {
		t.Fatalf("keyList 里没找到两个账号（quota=%v plain=%v）", quotaRow, plainRow)
	}
}

func mustCreateAccount(t *testing.T, g *Gateway, providerID, label string) *provider.AccountEnv {
	t.Helper()
	env, apiErr := g.createAccount(providerID, label, nil, "")
	if apiErr != nil {
		t.Fatalf("createAccount(%s): %s", label, apiErr.Message)
	}
	return env
}
