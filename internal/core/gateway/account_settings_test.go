package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

// TestAccountSettingsAndDuplicate 覆盖 local-vault 式账号运维：
// 同密钥账号打重复标记；账号级启停/优先级/权重/并发/模型白名单可写回。
func TestAccountSettingsAndDuplicate(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	mk := func(id, label, key string) *provider.AccountEnv {
		e := provider.NewAccountEnv(id, "mock-native")
		e.Label = label
		e.Credentials = map[string]string{"api_key": key}
		g.Pool().Add(e)
		return e
	}
	mk("dup-a", "A", "sk-same")
	mk("dup-b", "B", "sk-same")
	mk("uniq-c", "C", "sk-other")

	rec := httptest.NewRecorder()
	g.handleAccounts(rec, httptest.NewRequest(http.MethodGet, "/api/accounts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("handleAccounts status = %d", rec.Code)
	}
	var list struct {
		Accounts []struct {
			AccountID string `json:"account_id"`
			Label     string `json:"label"`
			Duplicate string `json:"duplicate_of"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("解析账号列表失败: %v", err)
	}
	dupOf := map[string]string{}
	for _, a := range list.Accounts {
		dupOf[a.AccountID] = a.Duplicate
	}
	if dupOf["dup-a"] != "" || dupOf["dup-b"] != "A" || dupOf["uniq-c"] != "" {
		t.Fatalf("重复标记错误: %+v", dupOf)
	}

	post := func(id, payload string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/accounts/"+id+"/settings", strings.NewReader(payload))
		req.SetPathValue("id", id)
		out := httptest.NewRecorder()
		g.handleAccountSettings(out, req)
		return out
	}
	rec = post("dup-b", `{"disabled":true,"priority":2,"weight":7,"concurrency":0,"allowed_models":["mock-native/echo-pro"," mock-native/echo-pro ",""]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings status = %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Account provider.AccountSnapshot `json:"account"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析设置结果失败: %v", err)
	}
	a := out.Account
	if !a.Disabled || a.Priority != 2 || a.Weight != 7 {
		t.Fatalf("启停/优先级/权重未落库: %+v", a)
	}
	if a.Concurrency == nil || *a.Concurrency != 0 {
		t.Fatalf("并发覆盖未落库: %+v", a.Concurrency)
	}
	if len(a.AllowedModels) != 1 || a.AllowedModels[0] != "mock-native/echo-pro" {
		t.Fatalf("模型白名单未去重清洗: %+v", a.AllowedModels)
	}

	rec = post("dup-b", `{"concurrency":-1,"disabled":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear settings status = %d", rec.Code)
	}
	out.Account = provider.AccountSnapshot{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	if out.Account.Concurrency != nil || out.Account.Disabled {
		t.Fatalf("并发覆盖/停用未清除: %+v", out.Account)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/accounts/missing/settings", strings.NewReader(`{}`))
	req.SetPathValue("id", "missing")
	rec = httptest.NewRecorder()
	g.handleAccountSettings(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的账号 status = %d", rec.Code)
	}
}
