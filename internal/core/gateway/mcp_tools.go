package gateway

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// 每个 MCP 服务器的工具开关（插件 servers/tools）：names 是“关掉”的工具。
// 关掉的工具在中继侧生效：tools/list 里隐藏，直接 tools/call 被拒绝。
// 直连（未登录）的本地/远程配置没有中继可用，开关只影响库里的记录与中继。

func (g *Gateway) mcpDisabledTools(name string) map[string]bool {
	for _, m := range g.library.List().MCPServers {
		if m.Name != name {
			continue
		}
		if len(m.DisabledTools) == 0 {
			return nil
		}
		out := make(map[string]bool, len(m.DisabledTools))
		for _, t := range m.DisabledTools {
			out[t] = true
		}
		return out
	}
	return nil
}

// handleMCPToolsSave POST /api/library/servers/tools {name, names}
func (g *Gateway) handleMCPToolsSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string   `json:"name"`
		Names []string `json:"names"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少服务器名"})
		return
	}
	var entryID string
	for _, m := range g.library.List().MCPServers {
		if m.Name == name {
			entryID = m.ID
			break
		}
	}
	if entryID == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "库中没有这个 MCP 服务器: " + name})
		return
	}
	off := make([]string, 0, len(body.Names))
	for _, n := range body.Names {
		if n = strings.TrimSpace(n); n != "" {
			off = append(off, n)
		}
	}
	g.library.UpdateMCPDisabledTools(entryID, dedupeStrings(off))
	g.writeLibraryView(w)
}

// mcpRelayCall 是转发前从 JSON-RPC 请求体里读到的关键信息。
type mcpRelayCall struct {
	method string
	tool   string
	id     json.RawMessage
}

func parseMCPRelayCall(body []byte) mcpRelayCall {
	var msg struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	if len(body) == 0 || json.Unmarshal(body, &msg) != nil {
		return mcpRelayCall{}
	}
	if len(msg.ID) == 0 {
		msg.ID = json.RawMessage("null")
	}
	return mcpRelayCall{method: msg.Method, tool: msg.Params.Name, id: msg.ID}
}

// filterMCPToolsMessage 从一条 JSON-RPC 响应里去掉被关掉的工具。
// 不是 tools/list 形状时原样返回。
func filterMCPToolsMessage(raw []byte, off map[string]bool) []byte {
	if len(off) == 0 {
		return raw
	}
	var msg map[string]json.RawMessage
	if json.Unmarshal(raw, &msg) != nil {
		return raw
	}
	resRaw, ok := msg["result"]
	if !ok {
		return raw
	}
	var res map[string]json.RawMessage
	if json.Unmarshal(resRaw, &res) != nil {
		return raw
	}
	toolsRaw, ok := res["tools"]
	if !ok {
		return raw
	}
	var tools []map[string]any
	if json.Unmarshal(toolsRaw, &tools) != nil {
		return raw
	}
	kept := make([]map[string]any, 0, len(tools))
	changed := false
	for _, t := range tools {
		n, _ := t["name"].(string)
		if off[n] {
			changed = true
			continue
		}
		kept = append(kept, t)
	}
	if !changed {
		return raw
	}
	tb, _ := json.Marshal(kept)
	res["tools"] = tb
	rb, _ := json.Marshal(res)
	msg["result"] = rb
	out, _ := json.Marshal(msg)
	return out
}

// filterMCPToolsSSE 逐行过滤 SSE 事件里的 tools/list 响应，保持流式。
func filterMCPToolsSSE(w io.Writer, body io.Reader, off map[string]bool) {
	br := bufio.NewReaderSize(body, 32<<10)
	flusher, _ := w.(http.Flusher)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			if strings.HasPrefix(line, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if out := filterMCPToolsMessage([]byte(payload), off); string(out) != payload {
					line = "data: " + string(out) + "\n"
				}
			}
			_, _ = io.WriteString(w, line)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}
