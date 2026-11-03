package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aether-cockpit/internal/core/library"
)

// GitHub 仓库技能安装：先探出仓库里的 SKILL.md，再按选中路径安装。

type skillCandidate struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
	Have        bool   `json:"have,omitempty"`
}

func normalizeGitHubRepo(source string) (string, error) {
	s := strings.TrimSpace(source)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if i := strings.Index(s, "/"); i >= 0 {
		host := s[:i]
		if strings.Contains(host, ".") {
			if host != "github.com" {
				return "", fmt.Errorf("只支持 GitHub 仓库地址（owner/repo）")
			}
			s = s[i+1:]
		}
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("只支持 GitHub 仓库地址（owner/repo）")
	}
	return parts[0] + "/" + parts[1], nil
}

// classifySkillSource 区分本机文件夹与 GitHub 仓库。
func classifySkillSource(source string) (kind, target string, err error) {
	s := strings.TrimSpace(source)
	if s == "" {
		return "", "", fmt.Errorf("请填写 GitHub 仓库或本机文件夹")
	}
	if p := expandHomePath(s); p != "" {
		if info, statErr := os.Stat(p); statErr == nil && info.IsDir() {
			abs, absErr := filepath.Abs(p)
			if absErr != nil {
				return "", "", absErr
			}
			return "local", abs, nil
		}
	}
	if repo, ok := gitLabRepo(s); ok {
		return "gitlab", repo, nil
	}
	repo, err := normalizeGitHubRepo(s)
	if err == nil && !strings.HasPrefix(strings.ToLower(s), "git+") {
		return "github", repo, nil
	}
	if target, ok := gitCloneTarget(s); ok {
		return "git", target, nil
	}
	if err != nil {
		return "", "", err
	}
	return "github", repo, nil
}

// probeLocalSkills 在本机文件夹里找 SKILL.md（跳过隐藏目录与常见依赖目录）。
func (g *Gateway) probeLocalSkills(root string) ([]skillCandidate, error) {
	known := map[string]bool{}
	for _, s := range g.library.List().Skills {
		known[s.Name] = true
	}
	out := []skillCandidate{}
	count := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "SKILL.md" || count >= 200 {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		dir := filepath.Dir(rel)
		if dir == "." {
			return nil
		}
		count++
		name := filepath.Base(dir)
		out = append(out, skillCandidate{Name: name, Path: filepath.ToSlash(rel), Have: known[name]})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

func (g *Gateway) installLocalSkills(root string, paths []string, agents []string) (map[string]any, error) {
	return g.installLocalSkillsAs(root, "local", "", paths, agents)
}

func (g *Gateway) installLocalSkillsAs(root, kind, repo string, paths []string, agents []string) (map[string]any, error) {
	installed, had, applied := []string{}, []string{}, []string{}
	skipped := []any{}
	known := map[string]bool{}
	for _, s := range g.library.List().Skills {
		known[s.Name] = true
	}
	for _, p := range paths {
		rel := filepath.Clean(filepath.FromSlash(strings.TrimSpace(p)))
		if rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") || strings.Contains(rel, ".."+string(filepath.Separator)) {
			skipped = append(skipped, map[string]any{"what": "skill:" + p, "error": "路径超出所选文件夹"})
			continue
		}
		full := filepath.Join(root, rel)
		if !strings.HasPrefix(full, root+string(filepath.Separator)) && full != root {
			skipped = append(skipped, map[string]any{"what": "skill:" + p, "error": "路径超出所选文件夹"})
			continue
		}
		if filepath.Base(full) != "SKILL.md" {
			skipped = append(skipped, map[string]any{"what": "skill:" + p, "error": "不是 SKILL.md"})
			continue
		}
		name := filepath.Base(filepath.Dir(full))
		if known[name] {
			had = append(had, name)
			continue
		}
		info, statErr := os.Stat(full)
		if statErr != nil || info.IsDir() {
			skipped = append(skipped, map[string]any{"what": "skill:" + name, "error": "读取失败"})
			continue
		}
		if info.Size() > 2<<20 {
			skipped = append(skipped, map[string]any{"what": "skill:" + name, "error": "文件超过 2 MiB"})
			continue
		}
		raw, readErr := os.ReadFile(full)
		if readErr != nil || strings.TrimSpace(string(raw)) == "" {
			skipped = append(skipped, map[string]any{"what": "skill:" + name, "error": "文件为空或不可读"})
			continue
		}
		content := string(raw)
		entry, err := g.library.SaveSkill(library.SkillEntry{
			Name: name, Description: skillDescription(content), Content: content,
			Kind: kind, Repo: repo, SkillPath: filepath.ToSlash(rel), InstalledContent: content,
		})
		if err != nil {
			skipped = append(skipped, map[string]any{"what": "skill:" + name, "error": err.Error()})
			continue
		}
		known[name] = true
		installed = append(installed, name)
		targets := map[string]bool{}
		for _, a := range agents {
			switch a {
			case "claude_code":
				targets["claude"] = true
			case "codex_cli", "gemini_cli", "opencode":
				targets["agents"] = true
			}
		}
		for t := range targets {
			link := g.library.EffectiveSkillHow(t) == library.SkillHowLink
			if _, err := g.library.ApplySkillAs(entry.ID, t, link); err == nil {
				applied = append(applied, t)
			}
		}
	}
	sort.Strings(applied)
	return map[string]any{"installed": installed, "had": had, "skipped": skipped, "applied": applied}, nil
}

func (g *Gateway) probeGitHubSkills(ctx context.Context, source string) (string, []skillCandidate, error) {
	repo, err := normalizeGitHubRepo(source)
	if err != nil {
		return "", nil, err
	}
	paths, err := g.githubTrees.skillPaths(ctx, marketGitHubAPI(), repo, g.marketGitHubToken())
	if err != nil {
		return "", nil, err
	}
	known := map[string]bool{}
	for _, s := range g.library.List().Skills {
		known[s.Name] = true
	}
	out := make([]skillCandidate, 0, len(paths))
	for _, p := range paths {
		dir := path.Dir(p)
		name := path.Base(dir)
		if name == "." || name == "/" || name == "" {
			name = repo
		}
		out = append(out, skillCandidate{Name: name, Path: p, Have: known[name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return repo, out, nil
}

// validSkillPath 校验仓库内路径：相对、无 . / .. 段、文件名必须是 SKILL.md。
func validSkillPath(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return path.Base(p) == "SKILL.md"
}

// readLimited 读取至多 limit 字节；超过时明确报错而不是静默截断。
func readLimited(r io.Reader, limit int64, what string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s 超过 %d MiB", what, limit>>20)
	}
	return raw, nil
}

func fetchGitHubRaw(ctx context.Context, rawBase, repo, p string) (string, error) {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	u := rawBase + "/" + repo + "/HEAD/" + strings.Join(segs, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("%w: %s", errSkillGone, p)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("读取 %s 失败: HTTP %d", p, resp.StatusCode)
	}
	raw, err := readLimited(resp.Body, 2<<20, p)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", fmt.Errorf("%s 为空", p)
	}
	return string(raw), nil
}

func (g *Gateway) installGitHubSkills(ctx context.Context, source string, paths []string, agents []string) (map[string]any, error) {
	repo, err := normalizeGitHubRepo(source)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, s := range g.library.List().Skills {
		known[s.Name] = true
	}
	installed := []string{}
	had := []string{}
	skipped := []any{}
	applied := []string{}
	for _, p := range paths {
		name := path.Base(path.Dir(p))
		if name == "." || name == "/" || name == "" {
			name = repo
		}
		if !validSkillPath(p) {
			skipped = append(skipped, map[string]any{"what": "skill:" + p, "error": "不是合法的 SKILL.md 路径"})
			continue
		}
		if known[name] {
			had = append(had, name)
			continue
		}
		content, err := fetchGitHubRaw(ctx, marketGitHubRaw(), repo, p)
		if err != nil {
			skipped = append(skipped, map[string]any{"what": "skill:" + name, "error": err.Error()})
			continue
		}
		entry, err := g.library.SaveSkill(library.SkillEntry{
			Name: name, Description: skillDescription(content), Content: content,
			Kind: "github", Repo: repo, SkillPath: p, InstalledContent: content,
		})
		if err != nil {
			skipped = append(skipped, map[string]any{"what": "skill:" + name, "error": err.Error()})
			continue
		}
		known[name] = true
		installed = append(installed, name)
		targets := map[string]bool{}
		for _, a := range agents {
			switch a {
			case "claude_code":
				targets["claude"] = true
			case "codex_cli", "gemini_cli", "opencode":
				targets["agents"] = true
			}
		}
		for t := range targets {
			link := g.library.EffectiveSkillHow(t) == library.SkillHowLink
			if _, err := g.library.ApplySkillAs(entry.ID, t, link); err == nil {
				applied = append(applied, t)
			}
		}
	}
	sort.Strings(applied)
	return map[string]any{
		"installed": installed, "had": had, "skipped": skipped, "applied": applied,
	}, nil
}

// handleSkillsProbe POST /api/library/skills/probe {source}
func (g *Gateway) handleSkillsProbe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source string `json:"source"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	kind, target, err := classifySkillSource(body.Source)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var candidates []skillCandidate
	display := target
	switch kind {
	case "github":
		display, candidates, err = g.probeGitHubSkills(ctx, target)
	case "gitlab":
		candidates, err = g.probeGitLabSkills(ctx, target)
	case "git":
		candidates, err = g.probeGitSkills(ctx, target)
	default:
		candidates, err = g.probeLocalSkills(target)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": display, "kind": kind, "candidates": candidates,
	})
}

// handleSkillsInstall POST /api/library/skills/install {source, paths, agents}
func (g *Gateway) handleSkillsInstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Source string   `json:"source"`
		Paths  []string `json:"paths"`
		Agents []string `json:"agents"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败"})
		return
	}
	if len(body.Paths) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "没有选择技能"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	kind, target, err := classifySkillSource(body.Source)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var result map[string]any
	switch kind {
	case "github":
		result, err = g.installGitHubSkills(ctx, target, body.Paths, body.Agents)
	case "gitlab":
		result, err = g.installGitLabSkills(ctx, target, body.Paths, body.Agents)
	case "git":
		result, err = g.installGitSkills(ctx, target, body.Paths, body.Agents)
	default:
		result, err = g.installLocalSkills(target, body.Paths, body.Agents)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	out := g.libraryPluginView(g.library.List())
	out["problems"] = []any{}
	out["result"] = result
	writeJSON(w, http.StatusOK, out)
}
