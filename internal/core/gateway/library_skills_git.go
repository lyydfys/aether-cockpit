package gateway

import (
	"aether-cockpit/internal/core/executil"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// 任意 git 仓库：浅克隆到临时目录，探技能/安装，结束后删除。
// 支持 git+https://… 强制按 git 处理（GitHub/GitLab 默认走 API）。

func gitCloneTarget(source string) (string, bool) {
	s := strings.TrimSpace(source)
	if strings.HasPrefix(strings.ToLower(s), "git+") {
		s = s[4:]
	}
	if s == "" {
		return "", false
	}
	if strings.HasPrefix(s, "git@") {
		return s, true
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", false
	}
	switch u.Scheme {
	case "http", "https", "ssh", "git":
		return s, true
	}
	return "", false
}

// gitCloneEnv 让 git 不弹交互提示；SSH 用 BatchMode，用户自己设了
// GIT_SSH_COMMAND 时不覆盖。
func gitCloneEnv(base []string) []string {
	out := append([]string(nil), base...)
	hasSSH := false
	for _, e := range out {
		if strings.HasPrefix(e, "GIT_SSH_COMMAND=") {
			hasSSH = true
			break
		}
	}
	if !hasSSH {
		out = append(out, "GIT_SSH_COMMAND=ssh -oBatchMode=yes")
	}
	return append(out, "GIT_TERMINAL_PROMPT=0")
}

func cloneGitRepo(ctx context.Context, source string) (string, error) {
	dir, err := os.MkdirTemp("", "aether-skill-git-")
	if err != nil {
		return "", err
	}
	target := filepath.Join(dir, "repo")
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--single-branch", "--no-tags", "--", source, target)
	executil.Hide(cmd)
	cmd.Env = gitCloneEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(dir)
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return "", fmt.Errorf("git clone 失败: %s", msg)
	}
	return dir, nil
}

func (g *Gateway) probeGitSkills(ctx context.Context, source string) ([]skillCandidate, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	dir, err := cloneGitRepo(ctx, source)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	return g.probeLocalSkills(filepath.Join(dir, "repo"))
}

func (g *Gateway) installGitSkills(ctx context.Context, source string, paths []string, agents []string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	dir, err := cloneGitRepo(ctx, source)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	return g.installLocalSkillsAs(filepath.Join(dir, "repo"), "git", source, paths, agents)
}

// fetchGitRepoRaw 克隆仓库后读取路径（供 check/update；代价高于 API 来源）。
func fetchGitRepoRaw(ctx context.Context, source, p string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	dir, err := cloneGitRepo(ctx, source)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	root := filepath.Join(dir, "repo")
	rel := filepath.Clean(filepath.FromSlash(strings.TrimSpace(p)))
	if rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("路径超出仓库")
	}
	full := filepath.Join(root, rel)
	if !strings.HasPrefix(full, root+string(filepath.Separator)) {
		return "", fmt.Errorf("路径超出仓库")
	}
	info, err := os.Stat(full)
	if err != nil {
		return "", fmt.Errorf("%w: %s", errSkillGone, p)
	}
	if info.IsDir() || info.Size() > 2<<20 {
		return "", fmt.Errorf("读取 %s 失败", p)
	}
	raw, err := os.ReadFile(full)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return "", fmt.Errorf("%s 为空或不可读", p)
	}
	return string(raw), nil
}
