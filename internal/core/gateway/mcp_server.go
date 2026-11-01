package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"aether-cockpit/internal/core/tools"
)

// 网关自带的 MCP 服务器（aether 的 “任何 Agent 都能挂同一份搜索”）：
// POST /mcp 走 Streamable HTTP 的 JSON-RPC，暴露 web_search / web_fetch /
// generate_image 三个工具。配置沿用设置页的“工具服务”，不额外保存密钥。
// 鉴权：设置了网关 API Key 时必须带 Bearer（或有效的调用方密钥）；否则只允许本机。

var mcpHTMLBlock = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<style[^>]*>.*?</style>`)
var mcpHTMLTag = regexp.MustCompile(`(?s)<[^>]*>`)

func (g *Gateway) mcpServerAllowed(r *http.Request) bool {
	if strings.TrimSpace(g.cfg.APIKey) != "" {
		tok := extractToken(r)
		if tok == g.cfg.APIKey {
			return true
		}
		if _, ok := g.callerKeys.match(tok); ok {
			return true
		}
		return false
	}
	return isLocalRequest(r)
}

func mcpJSONRPCError(id json.RawMessage, code int, msg string) map[string]any {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}
}

func mcpToolResult(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

func mcpToolDefs() []map[string]any {
	return []map[string]any{
		{
			"name": "web_search", "description": "Search the web through the search service configured in Aether Cockpit.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":       map[string]any{"type": "string", "description": "What to search for"},
					"max_results": map[string]any{"type": "integer", "description": "At most this many results (default 5, at most 20)"},
				},
				"required": []string{"query"},
			},
		},
		{
			"name": "web_fetch", "description": "Read a web page as plain text.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":       map[string]any{"type": "string", "description": "The page's http:// or https:// URL"},
					"max_chars": map[string]any{"type": "integer", "description": "At most this many characters (default 20000, at most 100000)"},
				},
				"required": []string{"url"},
			},
		},
		{
			"name": "generate_image", "description": "Generate an image through the image service configured in Aether Cockpit.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "description": "What the image should show"},
					"model":  map[string]any{"type": "string", "description": "Optional model name"},
				},
				"required": []string{"prompt"},
			},
		},
	}
}

// handleMCPServer POST /mcp
func (g *Gateway) handleMCPServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, http.StatusMethodNotAllowed, mcpJSONRPCError(nil, -32600, "MCP 服务器只接受 POST"))
		return
	}
	if !g.mcpServerAllowed(r) {
		writeJSON(w, http.StatusUnauthorized, mcpJSONRPCError(nil, -32001, "需要网关 API Key"))
		return
	}
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&msg); err != nil {
		writeJSON(w, http.StatusBadRequest, mcpJSONRPCError(nil, -32700, "请求体不是 JSON: "+err.Error()))
		return
	}
	switch msg.Method {
	case "initialize":
		writeJSON(w, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": msg.ID,
			"result": map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "aether-cockpit", "version": "1"},
			},
		})
	case "notifications/initialized", "notifications/cancelled":
		w.WriteHeader(http.StatusAccepted)
	case "ping":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{}})
	case "tools/list":
		writeJSON(w, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"tools": mcpToolDefs()},
		})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		text, isErr := g.callMCPTool(r.Context(), p.Name, p.Arguments)
		writeJSON(w, http.StatusOK, map[string]any{
			"jsonrpc": "2.0", "id": msg.ID, "result": mcpToolResult(text, isErr),
		})
	default:
		writeJSON(w, http.StatusOK, mcpJSONRPCError(msg.ID, -32601, "方法未实现: "+msg.Method))
	}
}

func (g *Gateway) callMCPTool(ctx context.Context, name string, raw json.RawMessage) (string, bool) {
	switch name {
	case "web_search":
		var a struct {
			Query      string `json:"query"`
			MaxResults int    `json:"max_results"`
		}
		_ = json.Unmarshal(raw, &a)
		cfg, ok := g.searchConfig()
		if !ok {
			return "尚未配置网络搜索：请在设置页填写 provider 与 API Key", true
		}
		res, err := tools.Search(ctx, cfg, a.Query, a.MaxResults)
		if err != nil {
			return err.Error(), true
		}
		b, _ := json.Marshal(map[string]any{"provider": cfg.Provider, "results": res})
		return string(b), false
	case "web_fetch":
		var a struct {
			URL      string `json:"url"`
			MaxChars int    `json:"max_chars"`
		}
		_ = json.Unmarshal(raw, &a)
		text, err := fetchPageText(ctx, a.URL, a.MaxChars)
		if err != nil {
			return err.Error(), true
		}
		return text, false
	case "generate_image":
		var a struct {
			Prompt string `json:"prompt"`
			Model  string `json:"model"`
		}
		_ = json.Unmarshal(raw, &a)
		cfg, ok := g.imageConfig()
		if !ok {
			return "图像生成未配置：请在设置页填写 base_url", true
		}
		if a.Model != "" {
			cfg.Model = a.Model
		}
		body, _ := json.Marshal(map[string]any{"prompt": a.Prompt})
		code, out, err := tools.GenerateImage(ctx, cfg, body)
		if err != nil {
			return err.Error(), true
		}
		if code < 200 || code >= 300 {
			return fmt.Sprintf("图像上游返回 %d: %s", code, clipText(string(out), 300)), true
		}
		return string(out), false
	}
	return "未知工具: " + name, true
}

// web_fetch 的 SSRF 底线：不读链路本地与常见云元数据地址；
// 重定向的每一跳同样校验，避免“公网 302 到 169.254.169.254”。
var mcpBlockedFetchIPs = []net.IP{
	net.ParseIP("169.254.169.254"),
	net.ParseIP("100.100.100.200"),
	net.ParseIP("fd00:ec2::254"),
}

func mcpBlockedFetchURL(u *url.URL) bool {
	host := u.Hostname()
	if host == "" {
		return true
	}
	check := func(ip net.IP) bool {
		if ip == nil {
			return false
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return true
		}
		for _, blocked := range mcpBlockedFetchIPs {
			if blocked.Equal(ip) {
				return true
			}
		}
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return check(ip)
	}
	if ips, err := net.LookupIP(host); err == nil {
		for _, ip := range ips {
			if check(ip) {
				return true
			}
		}
	}
	return false
}

var mcpFetchClient = &http.Client{
	Timeout: 25 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("重定向过多")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("只支持 http:// 或 https:// 的网页地址")
		}
		if mcpBlockedFetchURL(req.URL) {
			return fmt.Errorf("拒绝访问链路本地/云元数据地址: %s", req.URL.Host)
		}
		return nil
	},
}

// fetchPageText 把网页读成纯文本（只允许 http/https，最多 2MB）。
func fetchPageText(ctx context.Context, rawURL string, maxChars int) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("只支持 http:// 或 https:// 的网页地址")
	}
	if mcpBlockedFetchURL(u) {
		return "", fmt.Errorf("拒绝访问链路本地/云元数据地址: %s", u.Host)
	}
	if maxChars <= 0 {
		maxChars = 20000
	}
	if maxChars > 100000 {
		maxChars = 100000
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "AetherCockpit/1 web_fetch")
	resp, err := mcpFetchClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("读取网页失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("网页返回 %s", resp.Status)
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	text := string(raw)
	if strings.Contains(ct, "html") || strings.Contains(text, "<html") {
		text = mcpHTMLBlock.ReplaceAllString(text, " ")
		text = mcpHTMLTag.ReplaceAllString(text, " ")
		text = html.UnescapeString(text)
	}
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > maxChars {
		text = string(r[:maxChars])
	}
	return text, nil
}
