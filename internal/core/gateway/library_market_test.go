package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/library"
)

const registryFixture = `{"servers":[
 {"server":{"name":"acme/tool","title":"Acme Tool","description":"d1","version":"1.0.0","repository":{"url":"https://github.com/acme/tool"},"packages":[{"registryType":"npm","identifier":"@acme/tool","version":"1.0.0","transport":{"type":"stdio"}}]},"_meta":{"io.modelcontextprotocol.registry/official":{"isLatest":false}}},
 {"server":{"name":"acme/tool","title":"Acme Tool","description":"d2","version":"2.0.0","repository":{"url":"https://github.com/acme/tool"},"packages":[{"registryType":"npm","identifier":"@acme/tool","version":"2.0.0","transport":{"type":"stdio"}}]},"_meta":{"io.modelcontextprotocol.registry/official":{"isLatest":true}}},
 {"server":{"name":"acme/remote","version":"0.1.0","remotes":[{"type":"streamable-http","url":"https://r/mcp"}]},"_meta":{"io.modelcontextprotocol.registry/official":{"isLatest":true}}},
 {"server":{"name":"acme/needs-key","version":"1.0.0","packages":[{"registryType":"pypi","identifier":"needs-key","environmentVariables":[{"name":"API_KEY","isRequired":true,"isSecret":true,"description":"key"}]}]},"_meta":{"io.modelcontextprotocol.registry/official":{"isLatest":true}}},
 {"server":{"name":"acme/oci","version":"1.0.0","packages":[{"registryType":"oci","identifier":"x"}]},"_meta":{"io.modelcontextprotocol.registry/official":{"isLatest":true}}}
]}`

func TestRegistryItems(t *testing.T) {
	var payload struct {
		Servers []registryServer `json:"servers"`
	}
	if err := json.Unmarshal([]byte(registryFixture), &payload); err != nil {
		t.Fatal(err)
	}
	items, byID := registryItems(payload.Servers)
	if len(items) != 3 {
		t.Fatalf("items = %+v", items)
	}
	if _, ok := byID["acme/tool@2.0.0"]; !ok {
		t.Fatalf("未选最新版本: %v", byID)
	}
	if _, ok := byID["acme/tool@1.0.0"]; ok {
		t.Fatal("旧版本不应保留")
	}
	var tool, remote, needsKey marketItem
	for _, it := range items {
		switch it.Name {
		case "acme/tool":
			tool = it
		case "acme/remote":
			remote = it
		case "acme/needs-key":
			needsKey = it
		}
	}
	if tool.Publisher != "acme" || tool.Runs != "npx" || tool.Homepage == "" {
		t.Fatalf("tool = %+v", tool)
	}
	args := tool.Config["args"].([]string)
	if tool.Config["command"] != "npx" || len(args) != 2 || args[1] != "@acme/tool@2.0.0" {
		t.Fatalf("tool config = %v", tool.Config)
	}
	if remote.Transport != "http" || remote.Config["url"] != "https://r/mcp" {
		t.Fatalf("remote = %+v", remote)
	}
	if len(needsKey.Inputs) != 1 || !needsKey.Inputs[0].Required || needsKey.Inputs[0].Where != "env" {
		t.Fatalf("needsKey inputs = %+v", needsKey.Inputs)
	}
}

func TestFetchRegistryServers(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("search")
		_, _ = w.Write([]byte(registryFixture))
	}))
	defer srv.Close()
	servers, err := fetchRegistryServers(context.Background(), srv.URL, "acme")
	if err != nil || len(servers) != 5 {
		t.Fatalf("servers=%d err=%v", len(servers), err)
	}
	if gotQuery != "acme" {
		t.Fatalf("q = %q", gotQuery)
	}
}

func TestImportMarketServer(t *testing.T) {
	dir := t.TempDir()
	store := library.NewStoreWithHome(filepath.Join(dir, "library.json"), t.TempDir())
	g := &Gateway{library: store}
	var payload struct {
		Servers []registryServer `json:"servers"`
	}
	_ = json.Unmarshal([]byte(registryFixture), &payload)
	items, byID := registryItems(payload.Servers)
	g.marketCache.put("", items, byID)

	entry, _, err := g.importMarketServer("acme/tool@2.0.0", nil, nil)
	if err != nil {
		t.Fatalf("import tool: %v", err)
	}
	if entry.Name != "acme/tool" {
		t.Fatalf("entry = %+v", entry)
	}
	var cfg map[string]any
	_ = json.Unmarshal(entry.Config, &cfg)
	if cfg["command"] != "npx" {
		t.Fatalf("cfg = %v", cfg)
	}

	if _, _, err := g.importMarketServer("acme/needs-key@1.0.0", nil, nil); err == nil {
		t.Fatal("缺少必填项应报错")
	}
	entry, _, err = g.importMarketServer("acme/needs-key@1.0.0", map[string]string{"API_KEY": "k"}, nil)
	if err != nil {
		t.Fatalf("import needs-key: %v", err)
	}
	_ = json.Unmarshal(entry.Config, &cfg)
	env, _ := cfg["env"].(map[string]any)
	if env["API_KEY"] != "k" {
		t.Fatalf("env = %v", cfg["env"])
	}

	if _, _, err := g.importMarketServer("missing@1", nil, nil); err == nil {
		t.Fatal("未知 id 应报错")
	}
}

func TestMCPMarketCacheQuery(t *testing.T) {
	var c mcpMarketCache
	item := marketItem{Name: "a", ID: "a@1"}
	byID := map[string]marketItem{"a@1": item}
	c.put("fetch", []marketItem{item}, byID)
	if _, _, ok := c.get("other"); ok {
		t.Fatal("不同查询不应命中")
	}
	items, gotByID, ok := c.get("fetch")
	if !ok || len(items) != 1 || gotByID["a@1"].Name != "a" {
		t.Fatalf("同查询应命中: %v %v", items, ok)
	}
	if c.lookupByID()["a@1"].Name != "a" {
		t.Fatal("lookupByID 应能取到条目")
	}
}
