package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func providerViewOf(t *testing.T, g *Gateway, id string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("providers status = %d", rec.Code)
	}
	var body struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	for _, p := range body.Providers {
		if p["id"] == id {
			return p
		}
	}
	t.Fatalf("providers 里没有 %s", id)
	return nil
}

// 渠道编辑器的 Routing / Rate limited / Stays 三项必须真的落盘并回显——
// 之前 providerDraft 没有这些字段，保存时被静默丢掉。
func TestProviderRouteSaveAndEcho(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
	})
	mustCreateAccount(t, g, "mock-native", "route@example.com")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/provider/save",
		strings.NewReader(`{"id":"mock-native","routing":"rotate","sink":true,"affinity":"strict"}`))
	g.handleProviderSave(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	p := providerViewOf(t, g, "mock-native")
	if p["routing"] != "rotate" || p["sink"] != true || p["affinity"] != "strict" {
		t.Fatalf("回显不对: routing=%v sink=%v affinity=%v", p["routing"], p["sink"], p["affinity"])
	}

	// 清空（routing=""、affinity=""、sink=false）后条目应被删除，视图回到缺省。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/provider/save",
		strings.NewReader(`{"id":"mock-native","routing":"","sink":false,"affinity":""}`))
	g.handleProviderSave(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d: %s", rec.Code, rec.Body.String())
	}
	p = providerViewOf(t, g, "mock-native")
	if p["routing"] != "" || p["sink"] != false || p["affinity"] != "" {
		t.Fatalf("清空后应回到缺省值: %v", p)
	}
}

// 未知的 Routing / Stays 要明确拒绝，而不是静默套默认。
func TestProviderRouteRejectsUnknown(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
	})
	mustCreateAccount(t, g, "mock-native", "route@example.com")
	for _, body := range []string{
		`{"id":"mock-native","routing":"turbo"}`,
		`{"id":"mock-native","affinity":"forever"}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/provider/save", strings.NewReader(body))
		g.handleProviderSave(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400，got %d：%s", body, rec.Code, rec.Body.String())
		}
	}
}
