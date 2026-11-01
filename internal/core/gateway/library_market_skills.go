package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/library"
)

const (
	defaultSkillsAPI = "https://skills.sh/api/search"
	defaultGitHubAPI = "https://api.github.com"
	defaultGitHubRaw = "https://raw.githubusercontent.com"
)

type skillMarketItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	SkillID     string `json:"skillId"`
	Installs    int    `json:"installs"`
	Description string `json:"description,omitempty"`
	Have        string `json:"have,omitempty"`
}

type skillMarketCache struct {
	mu     sync.Mutex
	q      string
	items  []skillMarketItem
	at     time.Time
	loaded bool
}

func (c *skillMarketCache) get(q string) ([]skillMarketItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded || c.q != q || time.Since(c.at) > mcpMarketTTL {
		return nil, false
	}
	return append([]skillMarketItem(nil), c.items...), true
}

func (c *skillMarketCache) put(q string, items []skillMarketItem) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.q, c.items, c.at, c.loaded = q, items, time.Now(), true
}

func (c *skillMarketCache) find(source, id string) (skillMarketItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded || time.Since(c.at) > mcpMarketTTL {
		return skillMarketItem{}, false
	}
	for _, it := range c.items {
		if it.Source == source && it.SkillID == id {
			return it, true
		}
	}
	return skillMarketItem{}, false
}

func skillsAPIBase() string {
	if v := strings.TrimSpace(os.Getenv("AETHER_SKILLS_API")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultSkillsAPI
}

func marketGitHubAPI() string {
	if v := strings.TrimSpace(os.Getenv("AETHER_GITHUB_API")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultGitHubAPI
}

func marketGitHubRaw() string {
	if v := strings.TrimSpace(os.Getenv("AETHER_GITHUB_RAW")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultGitHubRaw
}

func fetchSkillSearch(ctx context.Context, api, q string) ([]skillMarketItem, error) {
	u := api + "?q=" + url.QueryEscape(strings.TrimSpace(q))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "aether-cockpit/market")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("skills.sh 返回 %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var payload struct {
		Skills []struct {
			ID       string `json:"id"`
			Source   string `json:"source"`
			SkillID  string `json:"skillId"`
			Name     string `json:"name"`
			Installs int    `json:"installs"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("skills.sh 回包解析失败: %w", err)
	}
	out := make([]skillMarketItem, 0, len(payload.Skills))
	for _, s := range payload.Skills {
		if s.Source == "" || s.SkillID == "" {
			continue
		}
		name := s.Name
		if name == "" {
			name = s.SkillID
		}
		out = append(out, skillMarketItem{
			ID: s.ID, Name: name, Source: s.Source, SkillID: s.SkillID, Installs: s.Installs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Installs > out[j].Installs })
	if len(out) > 40 {
		out = out[:40]
	}
	return out, nil
}

type githubTreeEntry struct {
	paths []string
	at    time.Time
}

type githubTreeCache struct {
	mu sync.Mutex
	m  map[string]githubTreeEntry
}

func (c *githubTreeCache) skillPaths(ctx context.Context, apiBase, source string) ([]string, error) {
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]githubTreeEntry{}
	}
	if e, ok := c.m[source]; ok && time.Since(e.at) < mcpMarketTTL {
		c.mu.Unlock()
		return append([]string(nil), e.paths...), nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/repos/"+source+"/git/trees/HEAD?recursive=1", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "aether-cockpit/market")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("GitHub 仓库不存在: %s", source)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub 返回 %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tree []struct {
			Path string `json:"path"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("GitHub 回包解析失败: %w", err)
	}
	paths := make([]string, 0, 8)
	for _, t := range payload.Tree {
		if strings.HasSuffix(t.Path, "/SKILL.md") || t.Path == "SKILL.md" {
			paths = append(paths, t.Path)
		}
	}
	c.mu.Lock()
	c.m[source] = githubTreeEntry{paths: append([]string(nil), paths...), at: time.Now()}
	c.mu.Unlock()
	return paths, nil
}

func pickSkillPath(paths []string, id string) string {
	want := strings.ToLower(id)
	best := ""
	for _, p := range paths {
		dir := strings.ToLower(strings.TrimSuffix(p, "/SKILL.md"))
		if dir == want || strings.HasSuffix(dir, "/"+want) {
			if best == "" || len(p) < len(best) {
				best = p
			}
		}
	}
	return best
}

func fetchSkillContentWith(ctx context.Context, cache *githubTreeCache, apiBase, rawBase, source, id string) (string, error) {
	paths, err := cache.skillPaths(ctx, apiBase, source)
	if err != nil {
		return "", err
	}
	p := pickSkillPath(paths, id)
	if p == "" {
		return "", fmt.Errorf("仓库 %s 中找不到技能 %s", source, id)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawBase+"/"+source+"/HEAD/"+p, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("读取 %s 失败: HTTP %d", p, resp.StatusCode)
	}
	raw, err := readLimited(resp.Body, 2<<20, p)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "", fmt.Errorf("技能文件为空: %s", p)
	}
	return string(raw), nil
}

func (g *Gateway) importMarketSkill(ctx context.Context, source, id string, agents []string) (library.SkillEntry, []string, error) {
	item, ok := g.skillCache.find(source, id)
	name := id
	if ok && item.Name != "" {
		name = item.Name
	}
	content, err := fetchSkillContentWith(ctx, &g.githubTrees, marketGitHubAPI(), marketGitHubRaw(), source, id)
	if err != nil {
		return library.SkillEntry{}, nil, err
	}
	skillPath := ""
	if paths, err := g.githubTrees.skillPaths(ctx, marketGitHubAPI(), source); err == nil {
		skillPath = pickSkillPath(paths, id)
	}
	entry, err := g.library.SaveSkill(library.SkillEntry{
		Name: name, Description: skillDescription(content), Content: content,
		Kind: "github", Repo: source, SkillPath: skillPath, InstalledContent: content,
	})
	if err != nil {
		return library.SkillEntry{}, nil, err
	}
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
	return entry, applied, nil
}
