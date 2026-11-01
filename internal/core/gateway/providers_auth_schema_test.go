package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	_ "aether-cockpit/internal/providers/antigravity"
	_ "aether-cockpit/internal/providers/claude"
	_ "aether-cockpit/internal/providers/openaicompat"
	_ "aether-cockpit/internal/providers/reverse"
)

// 接入向导依赖 /api/providers 下发 auth_form_schema / oauth / webview_login_url；
// 这三个字段一旦被过滤掉，前端就只能退化成“填 API Key”。
func TestProvidersExposeAuthSchema(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	rec := httptest.NewRecorder()
	g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Providers []struct {
			ID     string   `json:"id"`
			Auths  []string `json:"supported_auths"`
			Schema []struct {
				Key      string `json:"key"`
				Required bool   `json:"required"`
			} `json:"auth_form_schema"`
			OAuth bool `json:"oauth"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	var claude, antigravity, codex bool
	for _, p := range body.Providers {
		switch p.ID {
		case "claude-sub":
			hasPKCE := false
			for _, a := range p.Auths {
				if a == "oauth_pkce" {
					hasPKCE = true
				}
			}
			claude = len(p.Auths) >= 2 && len(p.Schema) > 0 && p.OAuth && hasPKCE
		case "antigravity":
			antigravity = p.OAuth && len(p.Schema) > 0
		case "codex":
			codex = p.OAuth && len(p.Auths) >= 2
		}
	}
	if !claude {
		t.Fatalf("claude-sub 未下发 auth 信息: %+v", body.Providers)
	}
	if !antigravity {
		t.Fatalf("antigravity 未下发 oauth/auth_form_schema")
	}
	if !codex {
		t.Fatalf("codex 未下发订阅 oauth")
	}
}

// 接入向导要看到“已内置但还没有账号”的 vendor 渠道（xAI/DeepSeek/Kimi…），
// 渠道页仍应隐藏空 vendor 行。
func TestProvidersIncludeEmptyVendors(t *testing.T) {
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
	})
	read := func(q string) map[string]struct {
		Auths []string `json:"supported_auths"`
		OAuth bool     `json:"oauth"`
	} {
		rec := httptest.NewRecorder()
		g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers"+q, nil))
		var body struct {
			Providers []struct {
				ID    string   `json:"id"`
				Auths []string `json:"supported_auths"`
				OAuth bool     `json:"oauth"`
			} `json:"providers"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析失败: %v", err)
		}
		out := map[string]struct {
			Auths []string `json:"supported_auths"`
			OAuth bool     `json:"oauth"`
		}{}
		for _, p := range body.Providers {
			out[p.ID] = struct {
				Auths []string `json:"supported_auths"`
				OAuth bool     `json:"oauth"`
			}{p.Auths, p.OAuth}
		}
		return out
	}
	hidden := read("")
	if _, ok := hidden["xai"]; ok {
		t.Fatalf("空 vendor xai 不应出现在渠道页默认响应里")
	}
	all := read("?include_empty=1")
	xai, ok := all["xai"]
	if !ok {
		t.Fatalf("include_empty=1 没有 xai")
	}
	hasDevice := false
	for _, a := range xai.Auths {
		if a == "device_code" {
			hasDevice = true
		}
	}
	if !hasDevice || !xai.OAuth {
		t.Fatalf("xai 未声明 device_code/oauth: %+v", xai)
	}
	if _, ok := all["deepseek"]; !ok {
		t.Fatalf("include_empty=1 没有 deepseek")
	}
}
