package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// handleMCPConfig POST /api/library/mcp/config
//
// 请求体：{"agent": "<agent id>", "names": [...]}；把库里的服务器渲染成该
// Agent 项目文件的片段（插件「Copy config…」用），返回 {text, file, skipped}。
func (g *Gateway) handleMCPConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent string   `json:"agent"`
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "请求体解析失败: " + err.Error()})
		return
	}
	if _, ok := projectMCPAgents[body.Agent]; !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "不支持的 Agent: " + body.Agent})
		return
	}
	cfgs := map[string]json.RawMessage{}
	for _, m := range g.library.List().MCPServers {
		cfgs[m.Name] = m.Config
	}
	text, file, skipped, err := renderMCPConfig(body.Agent, body.Names, cfgs)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text, "file": file, "skipped": skipped})
}

// renderMCPConfig 把 Claude 形状的服务器配置渲染成目标 Agent 的项目文件片段。
func renderMCPConfig(agent string, names []string, cfgs map[string]json.RawMessage) (string, string, []string, error) {
	t, ok := projectMCPAgents[agent]
	if !ok {
		return "", "", nil, fmt.Errorf("不支持的 Agent: %s", agent)
	}
	entries := map[string]any{}
	skipped := []string{}
	for _, name := range names {
		raw, ok := cfgs[name]
		if !ok {
			skipped = append(skipped, name)
			continue
		}
		var obj any
		if err := json.Unmarshal(raw, &obj); err != nil {
			skipped = append(skipped, name)
			continue
		}
		entry, err := mcpEntryFor(t.Format, obj)
		if err != nil {
			skipped = append(skipped, name)
			continue
		}
		entries[name] = entry
	}
	if len(entries) == 0 {
		return "", t.Rel, skipped, nil
	}
	if t.Format == "codex" {
		ordered := make([]string, 0, len(entries))
		for name := range entries {
			ordered = append(ordered, name)
		}
		sort.Strings(ordered)
		var b strings.Builder
		for i, name := range ordered {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(codexConfigBlock(name, entries[name].(map[string]any)))
		}
		return b.String(), t.Rel, skipped, nil
	}
	key := "mcpServers"
	if t.Format == "opencode" {
		key = "mcp"
	}
	out, err := json.MarshalIndent(map[string]any{key: entries}, "", "  ")
	if err != nil {
		return "", t.Rel, skipped, err
	}
	return string(out) + "\n", t.Rel, skipped, nil
}

func codexConfigBlock(name string, entry map[string]any) string {
	var b strings.Builder
	b.WriteString("[mcp_servers." + tomlString(name) + "]\n")
	seen := map[string]bool{}
	for _, k := range []string{"url", "http_headers", "command", "args", "env"} {
		v, ok := entry[k]
		if !ok || v == nil {
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			continue
		}
		if m, ok := v.(map[string]any); ok && len(m) == 0 {
			continue
		}
		if a, ok := v.([]any); ok && len(a) == 0 {
			continue
		}
		b.WriteString(k + " = " + tomlInline(v) + "\n")
		seen[k] = true
	}
	rest := make([]string, 0, len(entry))
	for k := range entry {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		b.WriteString(k + " = " + tomlInline(entry[k]) + "\n")
	}
	return b.String()
}
