package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"aether-cockpit/internal/core/library"
)

func gitlabAPIBase() string {
	if v := strings.TrimSpace(os.Getenv("AETHER_GITLAB_API")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://gitlab.com/api/v4"
}

func gitlabRawBase() string {
	if v := strings.TrimSpace(os.Getenv("AETHER_GITLAB_RAW")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://gitlab.com"
}

// gitLabRepo 从 GitLab 地址/owner/repo 取出 owner/repo。
func gitLabRepo(source string) (string, bool) {
	s := strings.TrimSpace(source)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	if !strings.HasPrefix(s, "gitlab.com/") {
		// 只有显式带主机时才可能是 GitLab；owner/repo 仍按 GitHub 处理。
		if i := strings.Index(s, "/"); i < 0 || !strings.Contains(s[:i], ".") {
			return "", false
		}
		return "", false
	}
	s = strings.TrimPrefix(s, "gitlab.com/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[1], true
}

func (g *Gateway) probeGitLabSkills(ctx context.Context, repo string) ([]skillCandidate, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	type gitlabEntry struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	entries := []gitlabEntry{}
	seen := map[string]bool{}
	page := "1"
	for i := 0; i < 10 && page != "" && page != "0"; i++ {
		u := gitlabAPIBase() + "/projects/" + url.PathEscape(repo) +
			"/repository/tree?recursive=true&per_page=100&page=" + url.QueryEscape(page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return nil, fmt.Errorf("GitLab 仓库不存在: %s", repo)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return nil, fmt.Errorf("GitLab 返回 %d", resp.StatusCode)
		}
		next := resp.Header.Get("X-Next-Page")
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		var pageEntries []gitlabEntry
		if err := json.Unmarshal(raw, &pageEntries); err != nil {
			return nil, fmt.Errorf("GitLab 回包解析失败: %w", err)
		}
		for _, e := range pageEntries {
			if !seen[e.Path] {
				seen[e.Path] = true
				entries = append(entries, e)
			}
		}
		if next == "" || next == "0" || next == page {
			break
		}
		page = next
	}
	known := map[string]bool{}
	for _, s := range g.library.List().Skills {
		known[s.Name] = true
	}
	out := []skillCandidate{}
	for _, e := range entries {
		if e.Type != "blob" || !strings.HasSuffix(e.Path, "/SKILL.md") {
			continue
		}
		name := path.Base(path.Dir(e.Path))
		out = append(out, skillCandidate{Name: name, Path: e.Path, Have: known[name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func fetchGitLabRaw(ctx context.Context, repo, p string) (string, error) {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i := range segs {
		segs[i] = url.PathEscape(segs[i])
	}
	u := gitlabRawBase() + "/" + repo + "/-/raw/HEAD/" + strings.Join(segs, "/")
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

// fetchRepoRaw 按技能来源取 raw，供 check/update 使用。
func fetchRepoRaw(ctx context.Context, kind, repo, p string) (string, error) {
	switch kind {
	case "gitlab":
		return fetchGitLabRaw(ctx, repo, p)
	case "git":
		return fetchGitRepoRaw(ctx, repo, p)
	}
	return fetchGitHubRaw(ctx, marketGitHubRaw(), repo, p)
}

func (g *Gateway) applySkillToAgents(entry library.SkillEntry, agents []string) []string {
	targets := map[string]bool{}
	for _, a := range agents {
		switch a {
		case "claude_code":
			targets["claude"] = true
		case "codex_cli", "gemini_cli", "opencode":
			targets["agents"] = true
		}
	}
	applied := []string{}
	for t := range targets {
		link := g.library.EffectiveSkillHow(t) == library.SkillHowLink
		if _, err := g.library.ApplySkillAs(entry.ID, t, link); err == nil {
			applied = append(applied, t)
		}
	}
	sort.Strings(applied)
	return applied
}

func (g *Gateway) installGitLabSkills(ctx context.Context, repo string, paths []string, agents []string) (map[string]any, error) {
	known := map[string]bool{}
	for _, s := range g.library.List().Skills {
		known[s.Name] = true
	}
	installed, had, applied := []string{}, []string{}, []string{}
	skipped := []any{}
	for _, p := range paths {
		if !validSkillPath(p) {
			skipped = append(skipped, map[string]any{"what": "skill:" + p, "error": "不是合法的 SKILL.md 路径"})
			continue
		}
		name := path.Base(path.Dir(p))
		if known[name] {
			had = append(had, name)
			continue
		}
		content, err := fetchGitLabRaw(ctx, repo, p)
		if err != nil {
			skipped = append(skipped, map[string]any{"what": "skill:" + name, "error": err.Error()})
			continue
		}
		entry, err := g.library.SaveSkill(library.SkillEntry{
			Name: name, Description: skillDescription(content), Content: content,
			Kind: "gitlab", Repo: repo, SkillPath: p, InstalledContent: content,
		})
		if err != nil {
			skipped = append(skipped, map[string]any{"what": "skill:" + name, "error": err.Error()})
			continue
		}
		known[name] = true
		installed = append(installed, name)
		applied = append(applied, g.applySkillToAgents(entry, agents)...)
	}
	sort.Strings(applied)
	return map[string]any{"installed": installed, "had": had, "skipped": skipped, "applied": applied}, nil
}
