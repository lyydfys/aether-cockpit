package gateway

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/library"
)

func gitTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	skill := filepath.Join(root, "skills", "one", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("# One"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "."},
		{"commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return root
}

func TestClassifyGitSources(t *testing.T) {
	cases := []struct {
		in, kind, target string
	}{
		{"git+https://github.com/acme/skills", "git", "https://github.com/acme/skills"},
		{"https://example.com/acme/skills.git", "git", "https://example.com/acme/skills.git"},
		{"git@github.com:acme/skills.git", "git", "git@github.com:acme/skills.git"},
		{"https://gitlab.com/acme/skills", "gitlab", "acme/skills"},
		{"acme/skills", "github", "acme/skills"},
	}
	for _, c := range cases {
		kind, target, err := classifySkillSource(c.in)
		if err != nil || kind != c.kind || target != c.target {
			t.Fatalf("%q -> kind=%s target=%s err=%v", c.in, kind, target, err)
		}
	}
	local := t.TempDir()
	if kind, _, err := classifySkillSource(local); err != nil || kind != "local" {
		t.Fatalf("local dir kind=%s err=%v", kind, err)
	}
}

func TestProbeAndInstallGitSkills(t *testing.T) {
	repo := gitTestRepo(t)
	home := t.TempDir()
	store := library.NewStoreWithHome(filepath.Join(t.TempDir(), "library.json"), home)
	g := &Gateway{library: store}

	cands, err := g.probeGitSkills(context.Background(), repo)
	if err != nil || len(cands) != 1 || cands[0].Path != "skills/one/SKILL.md" {
		t.Fatalf("candidates = %+v err=%v", cands, err)
	}
	res, err := g.installGitSkills(context.Background(), repo, []string{"skills/one/SKILL.md"}, []string{"claude_code"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res["installed"].([]string); len(got) != 1 || got[0] != "one" {
		t.Fatalf("installed = %v", got)
	}
	sk := store.List().Skills[0]
	if sk.Kind != "git" || sk.Repo != repo || sk.SkillPath != "skills/one/SKILL.md" {
		t.Fatalf("metadata = %+v", sk)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "one", "SKILL.md")); err != nil {
		t.Fatalf("Agent 侧文件缺失: %v", err)
	}

	content, err := fetchGitRepoRaw(context.Background(), repo, "skills/one/SKILL.md")
	if err != nil || content != "# One" {
		t.Fatalf("raw = %q err=%v", content, err)
	}
	if _, err := fetchGitRepoRaw(context.Background(), repo, "../evil"); err == nil {
		t.Fatal("路径穿越应被拒绝")
	}
}

func TestValidSkillPath(t *testing.T) {
	ok := map[string]bool{
		"skills/one/SKILL.md":  true,
		"SKILL.md":             true,
		"../evil/SKILL.md":     false,
		"/abs/SKILL.md":        false,
		"skills/one/README.md": false,
		"skills//SKILL.md":     false,
		"skills/./SKILL.md":    false,
		`skills\one\SKILL.md`:  false,
	}
	for p, want := range ok {
		if got := validSkillPath(p); got != want {
			t.Fatalf("validSkillPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestGitCloneEnv(t *testing.T) {
	env := gitCloneEnv([]string{"PATH=x"})
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "GIT_TERMINAL_PROMPT=0") || !strings.Contains(joined, "GIT_SSH_COMMAND=ssh -oBatchMode=yes") {
		t.Fatalf("env = %v", env)
	}
	custom := gitCloneEnv([]string{"GIT_SSH_COMMAND=ssh -i mine"})
	joined = strings.Join(custom, "\n")
	if strings.Contains(joined, "BatchMode") || !strings.Contains(joined, "GIT_SSH_COMMAND=ssh -i mine") {
		t.Fatalf("custom env = %v", custom)
	}
}
