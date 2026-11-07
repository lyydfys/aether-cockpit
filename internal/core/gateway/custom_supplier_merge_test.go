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

func newSupplierGateway(t *testing.T) *Gateway {
	t.Helper()
	dir := t.TempDir()
	return New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		SettingsPath:     filepath.Join(dir, "settings.json"),
		AccountStorePath: filepath.Join(dir, "accounts.json"),
	})
}

func saveSupplier(g *Gateway, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	g.handleProviderSave(rec, httptest.NewRequest(http.MethodPost, "/api/provider/save", strings.NewReader(body)))
	return rec
}

func supplierChannels(g *Gateway) []CustomChannel {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	out := make([]CustomChannel, len(g.settings.CustomChannels))
	copy(out, g.settings.CustomChannels)
	return out
}

func findSupplierChannel(chs []CustomChannel, id string) *CustomChannel {
	for i := range chs {
		if chs[i].ID == id {
			return &chs[i]
		}
	}
	return nil
}

// 新建自定义供应商 → 一条 custom-* 渠道 + 一个账号；/api/providers 可见，模型前缀是渠道名。
func TestCustomSupplierSaveCreatesChannelAndAccount(t *testing.T) {
	g := newSupplierGateway(t)
	body := `{"id":"my-relay","name":"My Relay","chat":"https://relay.example.com/v1/chat/completions",
		"key":"sk-1","models":["gpt-4o","claude-3-5"]}`
	rec := saveSupplier(g, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	chs := supplierChannels(g)
	if len(chs) != 1 || chs[0].ID != "my-relay" || chs[0].Name != "My Relay" {
		t.Fatalf("自定义渠道 = %+v", chs)
	}
	if chs[0].BaseURL != "https://relay.example.com/v1" || chs[0].Protocol != "openai" ||
		len(chs[0].Models) != 2 {
		t.Fatalf("渠道字段 = %+v", chs[0])
	}

	p, ok := provider.Get("custom-my-relay")
	if !ok {
		t.Fatal("自定义渠道未注册成 provider")
	}
	m := p.Manifest()
	if len(m.Models) != 2 || !strings.HasPrefix(m.Models[0].ID, "custom-my-relay/") {
		t.Fatalf("模型前缀不对：%+v", m.Models)
	}

	envs := g.pool.ListByProvider("custom-my-relay")
	if len(envs) != 1 {
		t.Fatalf("账号数 = %d, want 1", len(envs))
	}
	if envs[0].GetCredential("api_key") != "sk-1" ||
		envs[0].GetCredential("base_url") != "https://relay.example.com/v1" {
		t.Fatalf("账号凭据 = %+v", envs[0].Credentials)
	}
	// 不能再往内核协议渠道里塞账号。
	if n := len(g.pool.ListByProvider("openai-compat")); n != 0 {
		t.Fatalf("不应创建内核渠道账号，实际 %d 个", n)
	}

	// /api/providers 能看到它。
	rec2 := httptest.NewRecorder()
	g.respondProviders(rec2, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	var out struct {
		Providers []struct {
			ID     string `json:"id"`
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode providers: %v", err)
	}
	found := false
	for _, pv := range out.Providers {
		if pv.ID != "custom-my-relay" {
			continue
		}
		found = true
		// /api/providers 的 models 是渠道内模型名（不带渠道前缀）；
		// 完整 ID 前缀由上面的 Manifest 断言钉住。
		if len(pv.Models) != 2 || pv.Models[0].ID != "gpt-4o" {
			t.Fatalf("providers 里的模型 = %+v", pv.Models)
		}
	}
	if !found {
		t.Fatalf("/api/providers 里没有 custom-my-relay")
	}
}

// 同一个供应商再次保存：渠道/账号都不新增，只更新 key 与模型。
func TestCustomSupplierSaveIsIdempotent(t *testing.T) {
	g := newSupplierGateway(t)
	first := `{"id":"my-relay","name":"My Relay","chat":"https://relay.example.com/v1/chat/completions",
		"key":"sk-1","models":["gpt-4o"]}`
	if rec := saveSupplier(g, first); rec.Code != http.StatusOK {
		t.Fatalf("first save: %d %s", rec.Code, rec.Body.String())
	}

	second := `{"id":"my-relay","name":"My Relay","chat":"https://relay.example.com/v1/chat/completions",
		"key":"sk-2","models":["gpt-4o","o3"]}`
	if rec := saveSupplier(g, second); rec.Code != http.StatusOK {
		t.Fatalf("second save: %d %s", rec.Code, rec.Body.String())
	}

	if chs := supplierChannels(g); len(chs) != 1 {
		t.Fatalf("渠道数 = %d, want 1（幂等）", len(chs))
	}
	envs := g.pool.ListByProvider("custom-my-relay")
	if len(envs) != 1 {
		t.Fatalf("账号数 = %d, want 1（幂等）", len(envs))
	}
	if envs[0].GetCredential("api_key") != "sk-2" {
		t.Fatalf("key 未更新：%q", envs[0].GetCredential("api_key"))
	}
	ch := findSupplierChannel(supplierChannels(g), "my-relay")
	if ch == nil || len(ch.Models) != 2 {
		t.Fatalf("模型未更新：%+v", ch)
	}
}

// 不同供应商各一条渠道，互不影响。
func TestCustomSupplierDifferentSlugs(t *testing.T) {
	g := newSupplierGateway(t)
	for _, body := range []string{
		`{"id":"relay-a","name":"Relay A","chat":"https://a.example.com/v1/chat/completions","key":"ka","models":["m-a"]}`,
		`{"id":"relay-b","name":"Relay B","chat":"https://b.example.com/v1/chat/completions","key":"kb","models":["m-b"]}`,
	} {
		if rec := saveSupplier(g, body); rec.Code != http.StatusOK {
			t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
		}
	}
	chs := supplierChannels(g)
	if len(chs) != 2 {
		t.Fatalf("渠道数 = %d, want 2", len(chs))
	}
	for _, id := range []string{"custom-relay-a", "custom-relay-b"} {
		if envs := g.pool.ListByProvider(id); len(envs) != 1 {
			t.Fatalf("%s 账号数 = %d", id, len(envs))
		}
	}
	if envs := g.pool.ListByProvider("custom-relay-a"); envs[0].GetCredential("api_key") != "ka" {
		t.Fatalf("relay-a key = %q", envs[0].GetCredential("api_key"))
	}
}

// 删除自定义渠道：渠道消失、provider 下线，但账号保留（页面显示「渠道不存在」）。
func TestCustomSupplierDeleteKeepsAccounts(t *testing.T) {
	g := newSupplierGateway(t)
	body := `{"id":"my-relay","name":"My Relay","chat":"https://relay.example.com/v1/chat/completions","key":"sk-1","models":["gpt-4o"]}`
	if rec := saveSupplier(g, body); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/custom-channels/my-relay", nil)
	req.SetPathValue("id", "my-relay")
	rec := httptest.NewRecorder()
	g.handleCustomChannelDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if len(supplierChannels(g)) != 0 {
		t.Fatal("删除后渠道应消失")
	}
	if _, ok := provider.Get("custom-my-relay"); ok {
		t.Fatal("删除后 provider 应下线")
	}
	// 既有约定：账号不静默删除，留给页面显示「渠道不存在」。
	if envs := g.pool.ListByProvider("custom-my-relay"); len(envs) != 1 {
		t.Fatalf("删除后账号应保留，实际 %d 个", len(envs))
	}
}

// 老数据（账号挂在 openai-compat 上、带 base_url）不受新逻辑影响。
func TestCustomSupplierOldCoreAccountsUntouched(t *testing.T) {
	g := newSupplierGateway(t)
	old, apiErr := g.createAccount("openai-compat", "Old Relay",
		map[string]string{"api_key": "old-key", "base_url": "https://old.example.com/v1"}, "")
	if apiErr != nil {
		t.Fatalf("create legacy account: %v", apiErr)
	}

	body := `{"id":"new-relay","name":"New Relay","chat":"https://new.example.com/v1/chat/completions","key":"new-key","models":["m1"]}`
	if rec := saveSupplier(g, body); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	envs := g.pool.ListByProvider("openai-compat")
	if len(envs) != 1 || envs[0].AccountID != old.AccountID {
		t.Fatalf("老账号被改动：%+v", envs)
	}
	if envs[0].GetCredential("api_key") != "old-key" ||
		envs[0].GetCredential("base_url") != "https://old.example.com/v1" {
		t.Fatalf("老账号凭据被改动：%+v", envs[0].Credentials)
	}
}
