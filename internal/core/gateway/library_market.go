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
	"strconv"
	"strings"
	"sync"
	"time"

	"aether-cockpit/internal/core/library"
)

// 资源库「发现」：代理官方 MCP Registry，映射成插件的市场卡片，
// 并支持一键把条目写进库（可同时应用到选中的 Agent）。

const defaultMCPRegistry = "https://registry.modelcontextprotocol.io/v0"

const mcpMarketTTL = 10 * time.Minute

type marketInput struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	Secret      bool   `json:"secret,omitempty"`
	Where       string `json:"where"`
}

type marketItem struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Publisher   string         `json:"publisher,omitempty"`
	Homepage    string         `json:"homepage,omitempty"`
	Icon        string         `json:"icon,omitempty"`
	Transport   string         `json:"transport"`
	Runs        string         `json:"runs,omitempty"`
	Inputs      []marketInput  `json:"inputs,omitempty"`
	Have        string         `json:"have,omitempty"`
	OptIn       bool           `json:"optIn,omitempty"`
	Config      map[string]any `json:"-"`
}

type registryPackage struct {
	RegistryType string `json:"registryType"`
	Identifier   string `json:"identifier"`
	Version      string `json:"version"`
	Transport    struct {
		Type string `json:"type"`
	} `json:"transport"`
	EnvironmentVariables []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		IsRequired  bool   `json:"isRequired"`
		IsSecret    bool   `json:"isSecret"`
	} `json:"environmentVariables"`
}

type registryServer struct {
	Server struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Version     string `json:"version"`
		Repository  struct {
			URL string `json:"url"`
		} `json:"repository"`
		Packages []registryPackage `json:"packages"`
		Remotes  []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"remotes"`
	} `json:"server"`
	Meta map[string]struct {
		IsLatest bool `json:"isLatest"`
	} `json:"_meta"`
}

type mcpMarketCache struct {
	mu     sync.Mutex
	q      string
	items  []marketItem
	byID   map[string]marketItem
	at     time.Time
	loaded bool
}

func (c *mcpMarketCache) get(q string) ([]marketItem, map[string]marketItem, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded || c.q != q || time.Since(c.at) > mcpMarketTTL {
		return nil, nil, false
	}
	return append([]marketItem(nil), c.items...), c.byID, true
}

// lookupByID 只看缓存里的条目（不要求查询一致），供 about/入库使用。
func (c *mcpMarketCache) lookupByID() map[string]marketItem {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded || time.Since(c.at) > mcpMarketTTL {
		return nil
	}
	return c.byID
}

func (c *mcpMarketCache) put(q string, items []marketItem, byID map[string]marketItem) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.q, c.items, c.byID, c.at, c.loaded = q, items, byID, time.Now(), true
}

func registryBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("AETHER_MCP_REGISTRY")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultMCPRegistry
}

func fetchRegistryServers(ctx context.Context, base, q string) ([]registryServer, error) {
	u := base + "/servers?limit=24"
	if strings.TrimSpace(q) != "" {
		u += "&search=" + url.QueryEscape(strings.TrimSpace(q))
	}
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
		return nil, fmt.Errorf("MCP Registry 返回 %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var payload struct {
		Servers []registryServer `json:"servers"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("MCP Registry 回包解析失败: %w", err)
	}
	return payload.Servers, nil
}

func parseVersion(v string) []int {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimFunc(p, func(r rune) bool { return r < '0' || r > '9' }))
		if err != nil {
			out = append(out, 0)
			continue
		}
		out = append(out, n)
	}
	return out
}

func versionGreater(a, b string) bool {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var na, nb int
		if i < len(pa) {
			na = pa[i]
		}
		if i < len(pb) {
			nb = pb[i]
		}
		if na != nb {
			return na > nb
		}
	}
	return a > b
}

func registryItem(rs registryServer) (marketItem, bool) {
	it := marketItem{
		Name:        rs.Server.Name,
		Title:       rs.Server.Title,
		Description: rs.Server.Description,
		Homepage:    rs.Server.Repository.URL,
		Transport:   "stdio",
	}
	if it.Title == "" {
		it.Title = it.Name
	}
	if i := strings.Index(rs.Server.Name, "/"); i > 0 {
		it.Publisher = rs.Server.Name[:i]
	}
	it.ID = rs.Server.Name + "@" + rs.Server.Version

	for _, r := range rs.Server.Remotes {
		typ := strings.ToLower(strings.TrimSpace(r.Type))
		if typ == "streamable-http" || typ == "http" || typ == "sse" {
			it.Transport = "http"
			cfg := map[string]any{"url": r.URL}
			if typ == "sse" {
				cfg["type"] = "sse"
			} else {
				cfg["type"] = "http"
			}
			it.Config = cfg
			return it, true
		}
	}

	for _, p := range rs.Server.Packages {
		var cmd string
		var args []string
		switch strings.ToLower(p.RegistryType) {
		case "npm":
			cmd = "npx"
			ref := p.Identifier
			if p.Version != "" {
				ref += "@" + p.Version
			}
			args = []string{"-y", ref}
		case "pypi":
			cmd = "uvx"
			args = []string{p.Identifier}
		default:
			continue
		}
		it.Runs = cmd
		it.Config = map[string]any{"command": cmd, "args": args}
		for _, e := range p.EnvironmentVariables {
			if e.Name == "" {
				continue
			}
			it.Inputs = append(it.Inputs, marketInput{
				Key: e.Name, Label: e.Name, Description: e.Description,
				Required: e.IsRequired, Secret: e.IsSecret, Where: "env",
			})
		}
		return it, true
	}
	return marketItem{}, false
}

// registryItems 去重（同名取 isLatest 或更高版本），按名称排序。
func registryItems(servers []registryServer) ([]marketItem, map[string]marketItem) {
	best := map[string]registryServer{}
	for _, s := range servers {
		name := s.Server.Name
		if name == "" {
			continue
		}
		old, ok := best[name]
		if !ok {
			best[name] = s
			continue
		}
		curLatest := s.Meta["io.modelcontextprotocol.registry/official"].IsLatest
		oldLatest := old.Meta["io.modelcontextprotocol.registry/official"].IsLatest
		if curLatest != oldLatest {
			if curLatest {
				best[name] = s
			}
			continue
		}
		if versionGreater(s.Server.Version, old.Server.Version) {
			best[name] = s
		}
	}
	names := make([]string, 0, len(best))
	for n := range best {
		names = append(names, n)
	}
	sort.Strings(names)
	items := make([]marketItem, 0, len(names))
	byID := map[string]marketItem{}
	for _, n := range names {
		it, ok := registryItem(best[n])
		if !ok {
			continue
		}
		items = append(items, it)
		byID[it.ID] = it
	}
	return items, byID
}

// handleLibraryMarket 处理 /api/library/market/{path...}：
// servers 搜索、about 描述、server 一键入库；skills 暂为空。
func (g *Gateway) handleLibraryMarket(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")
	fail := func(status int, msg string) {
		writeJSON(w, status, map[string]any{"error": msg})
	}
	switch {
	case path == "servers" && r.Method == http.MethodGet:
		q := r.URL.Query().Get("q")
		items, _, ok := g.marketCache.get(q)
		var failedErr error
		if !ok {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			servers, err := fetchRegistryServers(ctx, registryBaseURL(), q)
			cancel()
			if err != nil {
				// 搜索失败仍可回退到上一次成功的结果；都没有时给出错误。
				if len(items) == 0 {
					writeJSON(w, http.StatusOK, map[string]any{
						"items": []any{}, "error": "MCP Registry 未响应：" + err.Error(),
					})
					return
				}
				failedErr = err
			} else {
				var byID map[string]marketItem
				items, byID = registryItems(servers)
				g.marketCache.put(q, items, byID)
			}
		}
		known := map[string]bool{}
		for _, m := range g.library.List().MCPServers {
			known[m.Name] = true
		}
		out := make([]marketItem, 0, len(items))
		for _, it := range items {
			if known[it.Name] {
				it.Have = it.Name
			}
			out = append(out, it)
		}
		resp := map[string]any{"items": out}
		if failedErr != nil {
			resp["failed"] = "MCP Registry"
			resp["cause"] = failedErr.Error()
		}
		writeJSON(w, http.StatusOK, resp)
	case path == "skills" && r.Method == http.MethodGet:
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(q) < 2 {
			writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
			return
		}
		items, ok := g.skillCache.get(q)
		if !ok {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			fetched, err := fetchSkillSearch(ctx, skillsAPIBase(), q)
			cancel()
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"items": []any{}, "error": "skills.sh 未响应：" + err.Error(),
				})
				return
			}
			g.skillCache.put(q, fetched)
			items = fetched
		}
		known := map[string]bool{}
		for _, s := range g.library.List().Skills {
			known[s.Name] = true
		}
		out := make([]skillMarketItem, 0, len(items))
		for _, it := range items {
			if known[it.Name] {
				it.Have = it.Name
			}
			out = append(out, it)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	case path == "skill" && r.Method == http.MethodPost:
		var body struct {
			Source string   `json:"source"`
			ID     string   `json:"id"`
			Agents []string `json:"agents"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			fail(http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		if _, _, err := g.importMarketSkill(r.Context(), body.Source, body.ID, body.Agents); err != nil {
			fail(http.StatusBadRequest, err.Error())
			return
		}
		g.writeLibraryView(w)
	case path == "about":
		var body struct {
			IDs []string `json:"ids"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		byID := g.marketCache.lookupByID()
		out := map[string]string{}
		for _, id := range body.IDs {
			if it, ok := byID[id]; ok {
				out[id] = it.Description
			}
		}
		writeJSON(w, http.StatusOK, out)
	case path == "server" && r.Method == http.MethodPost:
		var body struct {
			ID     string            `json:"id"`
			Values map[string]string `json:"values"`
			Agents []string          `json:"agents"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			fail(http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		if _, _, err := g.importMarketServer(body.ID, body.Values, body.Agents); err != nil {
			fail(http.StatusBadRequest, err.Error())
			return
		}
		g.writeLibraryView(w)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
	}
}

func (g *Gateway) importMarketServer(id string, values map[string]string, agents []string) (library.MCPEntry, []string, error) {
	byID := g.marketCache.lookupByID()
	if byID == nil {
		return library.MCPEntry{}, nil, fmt.Errorf("目录已刷新，请重新搜索后再添加")
	}
	item, ok := byID[id]
	if !ok {
		return library.MCPEntry{}, nil, fmt.Errorf("目录中没有这个服务器: %s", id)
	}
	cfg := map[string]any{}
	for k, v := range item.Config {
		cfg[k] = v
	}
	env := map[string]any{}
	if e, ok := cfg["env"].(map[string]any); ok {
		for k, v := range e {
			env[k] = v
		}
	}
	for _, in := range item.Inputs {
		v := strings.TrimSpace(values[in.Key])
		if v == "" {
			if in.Required {
				return library.MCPEntry{}, nil, fmt.Errorf("缺少必填项: %s", in.Label)
			}
			continue
		}
		env[in.Key] = v
	}
	if len(env) > 0 {
		cfg["env"] = env
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return library.MCPEntry{}, nil, err
	}
	entry, err := g.library.SaveMCP(library.MCPEntry{Name: item.Name, Config: raw})
	if err != nil {
		return library.MCPEntry{}, nil, err
	}
	targets := map[string]bool{}
	for _, a := range agents {
		switch a {
		case "claude_code":
			targets["claude"] = true
		case "codex_cli":
			targets["codex"] = true
		case "gemini_cli":
			targets["gemini"] = true
		case "opencode":
			targets["opencode"] = true
		}
	}
	applied := []string{}
	for t := range targets {
		if _, err := g.library.ApplyMCP(entry.ID, t); err == nil {
			applied = append(applied, t)
		}
	}
	sort.Strings(applied)
	return entry, applied, nil
}
