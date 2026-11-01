package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func groupAction(t *testing.T, g *Gateway, action, body string) map[string]any {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/groups/{action}", g.handleGroupAction)
	req := httptest.NewRequest(http.MethodPost, "/api/groups/"+action, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s = %d %s", action, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func groupList(out map[string]any) []map[string]any {
	switch v := out["groups"].(type) {
	case []map[string]any:
		return v
	case []any:
		res := make([]map[string]any, 0, len(v))
		for _, x := range v {
			if m, ok := x.(map[string]any); ok {
				res = append(res, m)
			}
		}
		return res
	}
	return nil
}

func groupOf(out map[string]any, id string) map[string]any {
	for _, m := range groupList(out) {
		if m["id"] == id {
			return m
		}
	}
	return nil
}

func TestGroupsSavePickArrangeDelete(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})

	out := groupAction(t, g, "save", `{"id":"my-group","name":"My Group","members":["mock-native/echo-pro","mock-weak/echo-lite"],"routing":"order"}`)
	grp := groupOf(out, "my-group")
	if grp == nil || grp["routing"] != "order" {
		t.Fatalf("saved group = %#v", out["groups"])
	}
	members := grp["members"].([]any)
	if len(members) != 2 {
		t.Fatalf("members = %#v", members)
	}
	if chain := g.models.VirtualGroups()["my-group"]; len(chain) != 2 || chain[0] != "mock-native" {
		t.Fatalf("chain = %#v", chain)
	}

	// 手动选择：排到链首、routing=manual。
	out = groupAction(t, g, "pick", `{"id":"my-group","pick":"mock-weak/echo-lite"}`)
	grp = groupOf(out, "my-group")
	if grp["routing"] != "manual" || grp["pick"] != "mock-weak/echo-lite" {
		t.Fatalf("picked = %#v", grp)
	}
	if chain := g.models.VirtualGroups()["my-group"]; chain[0] != "mock-weak" {
		t.Fatalf("picked chain = %#v", chain)
	}

	// 排序：另一个组排到前面。
	_ = groupAction(t, g, "save", `{"id":"second","name":"Second","members":["mock-native/echo-pro"],"routing":"order"}`)
	out = groupAction(t, g, "arrange", `{"order":["second","my-group"]}`)
	gs := groupList(out)
	if len(gs) == 0 || gs[0]["id"] != "second" {
		t.Fatalf("order = %#v", gs)
	}

	// 删除后既不在列表里，也不再参与路由。
	out = groupAction(t, g, "delete", `{"ids":["my-group"]}`)
	if groupOf(out, "my-group") != nil {
		t.Fatal("删除后仍出现在列表里")
	}
	if _, ok := g.models.VirtualGroups()["my-group"]; ok {
		t.Fatal("删除后仍参与路由")
	}
	// 重启（同一设置文件）后仍应保持删除。
	g2 := New(Config{SettingsPath: g.settingsPath, UsagePath: "-"})
	if groupOf(g2.buildGroups(), "my-group") != nil {
		t.Fatal("删除的组在重启后复活")
	}
}

func TestGroupsShareImport(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	_ = groupAction(t, g, "save", `{"id":"share-me","name":"Share Me","members":["mock-native/echo-pro","mock-weak/echo-lite"],"routing":"rotate"}`)

	out := groupAction(t, g, "share", `{"id":"share-me"}`)
	shared, _ := out["shared"].(string)
	if shared == "" || !strings.Contains(shared, "aether_group") {
		t.Fatalf("shared = %q", shared)
	}

	g2 := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	body, _ := json.Marshal(map[string]string{"text": shared})
	imported := groupAction(t, g2, "import", string(body))
	info, _ := imported["imported"].(map[string]any)
	if info == nil || info["name"] != "Share Me" {
		t.Fatalf("imported = %#v", imported["imported"])
	}
	grp := groupOf(imported, "share-me")
	if grp == nil || grp["routing"] != "rotate" {
		t.Fatalf("imported group = %#v", grp)
	}
}

func TestAutoGroupDefsFrom(t *testing.T) {
	manifests := []provider.ProviderManifest{
		{ID: "p1", Models: []ir.ModelMeta{{ID: "p1/shared", UpstreamModel: "shared"}}},
		{ID: "p2", Models: []ir.ModelMeta{{ID: "p2/shared", UpstreamModel: "shared"}}},
		{ID: "p3", Models: []ir.ModelMeta{{ID: "p3/only", UpstreamModel: "only"}}},
	}
	defs := autoGroupDefsFrom(manifests)
	if len(defs) != 1 || defs[0].ID != "auto/shared" || len(defs[0].Chain) != 2 {
		t.Fatalf("defs = %#v", defs)
	}
}

func TestGroupsFoundToggle(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	before := g.buildGroups()
	if before["found"] != true {
		t.Fatalf("默认 found = %#v", before["found"])
	}
	out := groupAction(t, g, "found", `{"on":false}`)
	for _, m := range groupList(out) {
		if m["auto"] == true {
			t.Fatalf("关闭后仍有自动组: %#v", m["id"])
		}
	}
	if out["found"] != false {
		t.Fatalf("found = %#v", out["found"])
	}
	if len(g.models.VirtualGroups()) == 0 && len(g.groupDefsForView()) > 0 {
		// 关闭只应移除自动组，用户/基线组仍在。
	}
}

func TestGroupsTemplateSteady(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	out := groupAction(t, g, "template", `{"kind":"steady","name":"Steady Test"}`)
	grp := groupOf(out, "steady-test")
	if grp == nil || len(grp["members"].([]any)) < 2 {
		t.Fatalf("template group = %#v", grp)
	}
}

func TestTemplatesView(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	out := g.buildGroups()
	tpls, _ := out["templates"].([]map[string]any)
	if len(tpls) < 2 {
		t.Fatalf("templates = %#v", out["templates"])
	}
	kinds := map[string]bool{}
	for _, tp := range tpls {
		kinds[tp["kind"].(string)] = true
	}
	if !kinds["steady"] || !kinds["thrifty"] {
		t.Fatalf("kinds = %#v", kinds)
	}
	// 未配置分类模型时不出 smart 模板。
	if kinds["smart"] {
		t.Fatal("未配置 intent_classify_model 不应提供 smart 模板")
	}
}

func TestGroupModelsIncludeCreditRate(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.settingsMu.Lock()
	g.settings.ProviderModelCredit = map[string]map[string]float64{"mock-native": {"echo-pro": 0.5}}
	g.settingsMu.Unlock()
	out := g.buildGroups()
	models, _ := out["models"].([]routingMember)
	found := false
	for _, m := range models {
		if m.ID == "mock-native/echo-pro" {
			found = true
			if m.CreditRate != 0.5 {
				t.Fatalf("credit_rate = %v, want 0.5", m.CreditRate)
			}
		}
	}
	if !found {
		t.Fatalf("models 里没有 mock-native/echo-pro: %+v", models)
	}
}

func TestSmartTemplateNeedsClassifier(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/groups/{action}", g.handleGroupAction)
	req := httptest.NewRequest(http.MethodPost, "/api/groups/template", strings.NewReader(`{"kind":"smart","name":"Smart Test"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未配置分类模型应 400，得到 %d %s", rec.Code, rec.Body.String())
	}

	g2 := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-", IntentClassifyModel: "mock-native/echo-pro"})
	mux = http.NewServeMux()
	mux.HandleFunc("POST /api/groups/{action}", g2.handleGroupAction)
	req = httptest.NewRequest(http.MethodPost, "/api/groups/template", strings.NewReader(`{"kind":"smart","name":"Smart Test"}`))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("配置后应成功，得到 %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if groupOf(out, "smart-test") == nil {
		t.Fatalf("smart group 未创建: %#v", out["groups"])
	}
}
