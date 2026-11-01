package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/library"
)

func TestSkillCheckAndUpdate(t *testing.T) {
	content := "# One"
	tree := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tree":[{"path":"skills/one/SKILL.md"}]}`))
	}))
	defer tree.Close()
	raw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(content))
	}))
	defer raw.Close()
	t.Setenv("AETHER_GITHUB_API", tree.URL)
	t.Setenv("AETHER_GITHUB_RAW", raw.URL)

	dir := t.TempDir()
	store := library.NewStoreWithHome(filepath.Join(dir, "library.json"), t.TempDir())
	g := &Gateway{library: store}
	res, err := g.installGitHubSkills(context.Background(), "acme/skills", []string{"skills/one/SKILL.md"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := res["installed"].([]string); len(got) != 1 || got[0] != "one" {
		t.Fatalf("installed = %v", got)
	}

	find := func() library.SkillEntry {
		for _, s := range store.List().Skills {
			if s.Name == "one" {
				return s
			}
		}
		t.Fatal("skill 不在库中")
		return library.SkillEntry{}
	}
	sk := find()
	if sk.Kind != "github" || sk.Repo != "acme/skills" || sk.SkillPath != "skills/one/SKILL.md" {
		t.Fatalf("metadata = %+v", sk)
	}

	g.checkGitHubSkills(context.Background())
	if c := g.skillCheckOf("one"); c == nil || c["status"] != "current" {
		t.Fatalf("check = %v", c)
	}

	content = "# One v2"
	g.checkGitHubSkills(context.Background())
	if c := g.skillCheckOf("one"); c == nil || c["status"] != "update" {
		t.Fatalf("check after change = %v", c)
	}
	changed, err := g.updateGitHubSkill(context.Background(), "one", false)
	if err != nil || !changed {
		t.Fatalf("update changed=%v err=%v", changed, err)
	}
	sk = find()
	if sk.Content != "# One v2" || sk.InstalledContent != "# One v2" {
		t.Fatalf("updated = %+v", sk)
	}

	// 本地改过：默认拒绝，replace 才覆盖，并留备份。
	sk.Content = "# mine"
	if _, err := store.SaveSkill(sk); err != nil {
		t.Fatal(err)
	}
	content = "# One v3"
	if _, err := g.updateGitHubSkill(context.Background(), "one", false); err == nil {
		t.Fatal("本地已修改时应拒绝")
	}
	changed, err = g.updateGitHubSkill(context.Background(), "one", true)
	if err != nil || !changed {
		t.Fatalf("replace changed=%v err=%v", changed, err)
	}
	if got := find().Content; got != "# One v3" {
		t.Fatalf("content = %q", got)
	}
	files, _ := os.ReadDir(store.BackupDir())
	if len(files) == 0 {
		t.Fatal("覆盖前应留备份")
	}
}
