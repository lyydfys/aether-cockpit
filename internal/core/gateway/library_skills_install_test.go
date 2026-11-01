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

func TestNormalizeGitHubRepo(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/skills": "acme/skills",
		"github.com/acme/skills/":        "acme/skills",
		"acme/skills.git":                "acme/skills",
	}
	for in, want := range cases {
		got, err := normalizeGitHubRepo(in)
		if err != nil || got != want {
			t.Fatalf("%q -> %q err=%v", in, got, err)
		}
	}
	if _, err := normalizeGitHubRepo("https://gitlab.com/a/b"); err == nil {
		t.Fatal("非 GitHub 应报错")
	}
}

func TestProbeAndInstallGitHubSkills(t *testing.T) {
	tree := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tree":[
			{"path":"skills/one/SKILL.md"},
			{"path":"skills/two/SKILL.md"},
			{"path":"skills/three/SKILL.md"}
		]}`))
	}))
	defer tree.Close()
	raw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/skills/HEAD/skills/two/SKILL.md":
			_, _ = w.Write([]byte("# Two"))
		case "/acme/skills/HEAD/skills/three/SKILL.md":
			_, _ = w.Write([]byte("# Three"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer raw.Close()
	t.Setenv("AETHER_GITHUB_API", tree.URL)
	t.Setenv("AETHER_GITHUB_RAW", raw.URL)

	dir := t.TempDir()
	home := t.TempDir()
	store := library.NewStoreWithHome(filepath.Join(dir, "library.json"), home)
	g := &Gateway{library: store}
	if _, err := store.SaveSkill(library.SkillEntry{Name: "one", Content: "# One"}); err != nil {
		t.Fatal(err)
	}

	repo, candidates, err := g.probeGitHubSkills(context.Background(), "https://github.com/acme/skills")
	if err != nil || repo != "acme/skills" || len(candidates) != 3 {
		t.Fatalf("probe repo=%s candidates=%d err=%v", repo, len(candidates), err)
	}
	if !candidates[0].Have || candidates[1].Have {
		t.Fatalf("have 标记不对: %+v", candidates)
	}

	res, err := g.installGitHubSkills(context.Background(), "acme/skills",
		[]string{"skills/one/SKILL.md", "skills/two/SKILL.md"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res["installed"].([]string)) != 1 || len(res["had"].([]string)) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if _, err := g.installGitHubSkills(context.Background(), "acme/skills",
		[]string{"skills/three/SKILL.md"}, []string{"claude_code"}); err != nil {
		t.Fatal(err)
	}
	if _, err := filepath.Glob(filepath.Join(home, ".claude", "skills", "three", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestProbeAndInstallLocalSkills(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"skills/one/SKILL.md", "nested/two/SKILL.md", ".git/three/SKILL.md"} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("# "+rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	kind, target, err := classifySkillSource(root)
	if err != nil || kind != "local" {
		t.Fatalf("classify kind=%s target=%s err=%v", kind, target, err)
	}
	home := t.TempDir()
	store := library.NewStoreWithHome(filepath.Join(t.TempDir(), "library.json"), home)
	g := &Gateway{library: store}
	cands, err := g.probeLocalSkills(target)
	if err != nil || len(cands) != 2 {
		t.Fatalf("candidates = %+v err=%v", cands, err)
	}
	for _, c := range cands {
		if c.Name == "three" {
			t.Fatal(".git 里的技能不应出现")
		}
	}
	res, err := g.installLocalSkills(target, []string{"skills/one/SKILL.md"}, []string{"claude_code"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res["installed"].([]string); len(got) != 1 || got[0] != "one" {
		t.Fatalf("installed = %v", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "one", "SKILL.md")); err != nil {
		t.Fatalf("Agent 侧文件缺失: %v", err)
	}

	res, err = g.installLocalSkills(target, []string{"../evil/SKILL.md"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res["skipped"].([]any)) != 1 {
		t.Fatalf("穿越路径应被拒绝: %+v", res)
	}
}
