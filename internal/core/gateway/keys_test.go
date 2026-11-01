package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/core/transport"
)

func newKeysGateway(t *testing.T) *Gateway {
	t.Helper()
	g := &Gateway{
		settingsPath: filepath.Join(t.TempDir(), "settings.json"),
		pool:         pool.New(),
		models:       NewModelRouter(),
		tr:           transport.NewFactory(""),
	}
	return g
}

func postKeys(t *testing.T, g *Gateway, action, body string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/keys/"+action, strings.NewReader(body))
	req.SetPathValue("action", action)
	g.handleKeysAction(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestKeysAddToggleRenameReplaceRemove(t *testing.T) {
	g := newKeysGateway(t)
	out := postKeys(t, g, "add", `{"id":"openai-compat","name":"Team A","key":"sk-aaa"}`)
	if out["error"] != nil {
		t.Fatalf("add: %v", out["error"])
	}
	envs := g.pool.ListByProvider("openai-compat")
	if len(envs) != 1 || envs[0].Label != "Team A" || envs[0].GetCredential("api_key") != "sk-aaa" {
		t.Fatalf("account = %+v", envs)
	}
	id := envs[0].AccountID

	// off → 不可用
	postKeys(t, g, "off", `{"id":"openai-compat","ref":"`+id+`"}`)
	if envs[0].Available() {
		t.Fatal("off 后账号仍可用")
	}
	// on → 恢复
	postKeys(t, g, "on", `{"id":"openai-compat","ref":"`+id+`"}`)
	if !envs[0].Available() {
		t.Fatal("on 后账号未恢复")
	}

	postKeys(t, g, "rename", `{"id":"openai-compat","ref":"`+id+`","name":"Team B"}`)
	if envs[0].Label != "Team B" {
		t.Fatalf("rename = %q", envs[0].Label)
	}
	postKeys(t, g, "replace", `{"id":"openai-compat","ref":"`+id+`","key":"sk-bbb"}`)
	if envs[0].GetCredential("api_key") != "sk-bbb" {
		t.Fatal("replace 未生效")
	}
	postKeys(t, g, "remove", `{"id":"openai-compat","ref":"`+id+`"}`)
	if len(g.pool.ListByProvider("openai-compat")) != 0 {
		t.Fatal("remove 未删除账号")
	}
}

func TestKeyListInProvidersPayload(t *testing.T) {
	g := newKeysGateway(t)
	postKeys(t, g, "add", `{"id":"openai-compat","name":"A","key":"sk-1"}`)
	rec := httptest.NewRecorder()
	g.respondProviders(rec, httptest.NewRequest("GET", "/api/providers", nil))
	var payload struct {
		Providers []struct {
			ID      string `json:"id"`
			KeyList []struct {
				Name string `json:"name"`
				On   bool   `json:"on"`
			} `json:"keyList"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("parse: %v", err)
	}
	var found bool
	for _, p := range payload.Providers {
		if p.ID == "openai-compat" {
			found = len(p.KeyList) == 1 && p.KeyList[0].Name == "A" && p.KeyList[0].On
		}
	}
	if !found {
		t.Fatalf("providers 里没有 keyList: %s", rec.Body.String()[:300])
	}
}

func TestKeyUseAndModelAllowlist(t *testing.T) {
	g := newKeysGateway(t)
	postKeys(t, g, "add", `{"id":"openai-compat","name":"A","key":"sk-a"}`)
	postKeys(t, g, "add", `{"id":"openai-compat","name":"B","key":"sk-b"}`)
	envs := g.pool.ListByProvider("openai-compat")
	a, b := envs[0], envs[1]

	// 显式首选 B
	postKeys(t, g, "use", `{"id":"openai-compat","ref":"`+b.AccountID+`"}`)
	if b.Priority() != 1 || a.Priority() != 0 {
		t.Fatalf("priority a=%d b=%d", a.Priority(), b.Priority())
	}
	got, err := g.pool.Select("openai-compat", pool.SelectOptions{})
	if err != nil || got.AccountID != b.AccountID {
		t.Fatalf("Select = %v, %v（应选首选 B）", got, err)
	}

	// B 只服务 model-x；请求 model-y 时必须落到 A
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/provider/accountmodels", strings.NewReader(`{"id":"openai-compat","account":"`+b.AccountID+`","allow":["model-x"]}`))
	g.handleProviderAccountModels(rec, req)
	if rec.Code != 200 {
		t.Fatalf("accountmodels: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := g.pool.Select("openai-compat", pool.SelectOptions{Model: "model-y"}); got == nil || got.AccountID != a.AccountID {
		t.Fatalf("model-y 应落到 A，实际 %+v", got)
	}
	if got, _ := g.pool.Select("openai-compat", pool.SelectOptions{Model: "model-x"}); got == nil || got.AccountID != b.AccountID {
		t.Fatalf("model-x 应落到首选 B，实际 %+v", got)
	}
}
