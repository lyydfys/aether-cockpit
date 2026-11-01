package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/library"
)

func TestProbeAndInstallGitLabSkills(t *testing.T) {
	content := "# One"
	tree := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.RequestURI, "projects/acme%2Fskills") {
			t.Errorf("tree uri = %s", r.RequestURI)
		}
		_, _ = w.Write([]byte(`[
			{"path":"skills/one/SKILL.md","type":"blob"},
			{"path":"README.md","type":"blob"},
			{"path":"skills/empty","type":"tree"}
		]`))
	}))
	defer tree.Close()
	raw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/acme/skills/-/raw/HEAD/skills/one/SKILL.md" {
			t.Errorf("raw path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(content))
	}))
	defer raw.Close()
	t.Setenv("AETHER_GITLAB_API", tree.URL)
	t.Setenv("AETHER_GITLAB_RAW", raw.URL)
	t.Setenv("AETHER_GITHUB_API", tree.URL)
	t.Setenv("AETHER_GITHUB_RAW", raw.URL)

	kind, repo, err := classifySkillSource("https://gitlab.com/acme/skills")
	if err != nil || kind != "gitlab" || repo != "acme/skills" {
		t.Fatalf("classify kind=%s repo=%s err=%v", kind, repo, err)
	}

	dir := t.TempDir()
	store := library.NewStoreWithHome(filepath.Join(dir, "library.json"), t.TempDir())
	g := &Gateway{library: store}
	cands, err := g.probeGitLabSkills(context.Background(), "acme/skills")
	if err != nil || len(cands) != 1 || cands[0].Name != "one" {
		t.Fatalf("candidates = %+v err=%v", cands, err)
	}

	res, err := g.installGitLabSkills(context.Background(), "acme/skills", []string{"skills/one/SKILL.md"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := res["installed"].([]string); len(got) != 1 || got[0] != "one" {
		t.Fatalf("installed = %v", got)
	}
	sk := store.List().Skills[0]
	if sk.Kind != "gitlab" || sk.Repo != "acme/skills" || sk.InstalledContent != "# One" {
		t.Fatalf("metadata = %+v", sk)
	}

	g.checkGitHubSkills(context.Background())
	if c := g.skillCheckOf("one"); c == nil || c["status"] != "current" {
		t.Fatalf("check = %v", c)
	}
	content = "# One v2"
	changed, err := g.updateGitHubSkill(context.Background(), "one", false)
	if err != nil || !changed {
		t.Fatalf("update changed=%v err=%v", changed, err)
	}
	if store.List().Skills[0].Content != "# One v2" {
		t.Fatal("内容未更新")
	}
}

func TestGitLabPagination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("X-Next-Page", "2")
			_, _ = w.Write([]byte(`[{"path":"a/one/SKILL.md","type":"blob"}]`))
		case "2":
			_, _ = w.Write([]byte(`[{"path":"b/two/SKILL.md","type":"blob"}]`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	t.Setenv("AETHER_GITLAB_API", srv.URL)
	store := library.NewStoreWithHome(filepath.Join(t.TempDir(), "library.json"), t.TempDir())
	g := &Gateway{library: store}
	cands, err := g.probeGitLabSkills(context.Background(), "acme/skills")
	if err != nil || len(cands) != 2 {
		t.Fatalf("candidates = %+v err=%v", cands, err)
	}
	if cands[0].Name != "one" || cands[1].Name != "two" {
		t.Fatalf("candidates = %+v", cands)
	}
}
