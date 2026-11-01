package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/library"
)

func TestFetchSkillSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "review" {
			t.Errorf("q = %q", r.URL.Query().Get("q"))
		}
		_, _ = w.Write([]byte(`{"skills":[
			{"id":"a/r/one","source":"a/r","skillId":"one","name":"one","installs":10},
			{"id":"b/r/two","source":"b/r","skillId":"two","name":"two","installs":50},
			{"id":"bad","source":"","skillId":"","name":"bad","installs":99}
		]}`))
	}))
	defer srv.Close()
	items, err := fetchSkillSearch(context.Background(), srv.URL, "review")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].SkillID != "two" || items[1].SkillID != "one" {
		t.Fatalf("items = %+v", items)
	}
}

func TestFetchSkillContent(t *testing.T) {
	tree := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/skills/git/trees/HEAD" {
			t.Errorf("tree path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"tree":[
			{"path":"skills/engineering/code-review/SKILL.md"},
			{"path":"docs/SKILL.md"}
		]}`))
	}))
	defer tree.Close()
	raw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme/skills/HEAD/skills/engineering/code-review/SKILL.md" {
			t.Errorf("raw path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte("---\ndescription: code review skill\n---\n# Code Review"))
	}))
	defer raw.Close()

	var cache githubTreeCache
	content, err := fetchSkillContentWith(context.Background(), &cache, tree.URL, raw.URL, "acme/skills", "code-review")
	if err != nil {
		t.Fatal(err)
	}
	if content == "" || skillDescription(content) != "code review skill" {
		t.Fatalf("content = %q desc=%q", content, skillDescription(content))
	}
	if _, err := fetchSkillContentWith(context.Background(), &cache, tree.URL, raw.URL, "acme/skills", "missing"); err == nil {
		t.Fatal("找不到技能应报错")
	}
}

func TestImportMarketSkill(t *testing.T) {
	tree := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tree":[{"path":"skills/code-review/SKILL.md"}]}`))
	}))
	defer tree.Close()
	raw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# Code Review\nbody"))
	}))
	defer raw.Close()
	t.Setenv("AETHER_GITHUB_API", tree.URL)
	t.Setenv("AETHER_GITHUB_RAW", raw.URL)

	dir := t.TempDir()
	store := library.NewStoreWithHome(filepath.Join(dir, "library.json"), t.TempDir())
	g := &Gateway{library: store}
	g.skillCache.put("review", []skillMarketItem{
		{ID: "acme/skills/code-review", Name: "code-review", Source: "acme/skills", SkillID: "code-review", Installs: 1},
	})
	entry, _, err := g.importMarketSkill(context.Background(), "acme/skills", "code-review", nil)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Name != "code-review" || entry.Content == "" {
		t.Fatalf("entry = %+v", entry)
	}
	if _, _, err := g.importMarketSkill(context.Background(), "acme/skills", "nope", nil); err == nil {
		t.Fatal("找不到技能应报错")
	}
}

func TestSkillMarketCacheQuery(t *testing.T) {
	var c skillMarketCache
	c.put("a", []skillMarketItem{{Name: "one"}})
	if _, ok := c.get("b"); ok {
		t.Fatal("不同查询不应命中")
	}
	if items, ok := c.get("a"); !ok || len(items) != 1 {
		t.Fatal("同查询应命中")
	}
	c.put("b", []skillMarketItem{{Name: "two"}})
	if _, ok := c.find("", "one"); ok {
		t.Fatal("旧查询的条目不应被 find 命中")
	}
}
