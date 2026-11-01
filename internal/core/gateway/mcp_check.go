package gateway

import (
	"aether-cockpit/internal/core/executil"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MCP 服务器健康检查（magpie 的 mcp/check）：对库里已启用的服务器做一次
// initialize + tools/list，把状态交给资源库页面显示。
//
// 只检查远程 streamable HTTP：stdio 服务器需要启动任意用户命令，Aether
// Cockpit 不在网关进程里代为启动；这类服务器不出现在结果里，页面保持
// "not checked"。

type mcpCheckResult struct {
	State    string   `json:"state"`
	Tools    int      `json:"tools,omitempty"`
	ToolList []string `json:"toolList,omitempty"`
	Why      string   `json:"why,omitempty"`
	Code     int      `json:"code,omitempty"`
	Detail   string   `json:"detail,omitempty"`
	Step     string   `json:"step,omitempty"`
	Waited   int64    `json:"waited,omitempty"`
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func mcpNetWhy(err error, waited time.Duration) (string, int64) {
	ms := waited.Milliseconds()
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", ms
	}
	if strings.Contains(err.Error(), "refused") || strings.Contains(err.Error(), "actively refused") {
		return "refused", ms
	}
	return "unreachable", ms
}

func mcpPost(ctx context.Context, client *http.Client, url string, headers map[string]string, body string) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	return resp.StatusCode, resp.Header, raw, nil
}

func parseMCPTools(raw []byte) ([]string, bool) {
	candidates := [][]byte{raw}
	if bytes.Contains(raw, []byte("data:")) {
		candidates = nil
		sc := bufio.NewScanner(bytes.NewReader(raw))
		sc.Buffer(make([]byte, 0, 64<<10), 512<<10)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.HasPrefix(line, "data:") {
				candidates = append(candidates, []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))))
			}
		}
	}
	for _, c := range candidates {
		var msg struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if json.Unmarshal(c, &msg) == nil && msg.Result.Tools != nil {
			names := make([]string, 0, len(msg.Result.Tools))
			for _, t := range msg.Result.Tools {
				names = append(names, t.Name)
			}
			return names, true
		}
	}
	return nil, false
}

func checkMCPHTTP(ctx context.Context, url string, headers map[string]string) mcpCheckResult {
	start := time.Now()
	client := &http.Client{}
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"aether-cockpit","version":"1"}}}`
	code, respHeaders, raw, err := mcpPost(ctx, client, url, headers, initBody)
	if err != nil {
		why, waited := mcpNetWhy(err, time.Since(start))
		return mcpCheckResult{Why: why, Step: "initialize", Detail: clipText(err.Error(), 300), Waited: waited}
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return mcpCheckResult{State: "auth", Code: code, Detail: clipText(string(raw), 300)}
	}
	if code < 200 || code >= 300 {
		return mcpCheckResult{Why: "http", Code: code, Step: "initialize", Detail: clipText(string(raw), 300)}
	}
	if sid := respHeaders.Get("Mcp-Session-Id"); sid != "" {
		headers = map[string]string{}
		for k, v := range headers {
			headers[k] = v
		}
		headers["Mcp-Session-Id"] = sid
	}
	_, _, _, _ = mcpPost(ctx, client, url, headers, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	code, _, raw, err = mcpPost(ctx, client, url, headers, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if err != nil {
		why, waited := mcpNetWhy(err, time.Since(start))
		return mcpCheckResult{Why: why, Step: "tools/list", Detail: clipText(err.Error(), 300), Waited: waited}
	}
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return mcpCheckResult{State: "auth", Code: code, Detail: clipText(string(raw), 300)}
	}
	if code < 200 || code >= 300 {
		return mcpCheckResult{Why: "http", Code: code, Step: "tools/list", Detail: clipText(string(raw), 300)}
	}
	names, ok := parseMCPTools(raw)
	if !ok {
		return mcpCheckResult{Why: "protocol", Step: "tools/list", Detail: clipText(string(raw), 300)}
	}
	return mcpCheckResult{State: "ok", Tools: len(names), ToolList: names}
}

// cacheableMCPResult 决定结果能否复用：novar 只是本地缺少环境变量，
// 用户补上变量后应立即重检成功，缓存 5 分钟会让界面一直显示同一个错。
func cacheableMCPResult(res mcpCheckResult) bool {
	return res.Why != "novar"
}

func (g *Gateway) cacheMCPCheck(key string, res mcpCheckResult) {
	if !cacheableMCPResult(res) {
		return
	}
	g.mcpCache.put(key, res)
}

// handleMCPCheck POST /api/library/mcp/check
//
// 请求体：{"names": [...], "fresh": bool}；响应：{"servers": {name: 结果}}。
func (g *Gateway) handleMCPCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Names []string `json:"names"`
		Fresh bool     `json:"fresh"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)

	cfgs := map[string]json.RawMessage{}
	for _, m := range g.library.List().MCPServers {
		cfgs[m.Name] = m.Config
	}

	out := map[string]mcpCheckResult{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, name := range body.Names {
		raw, ok := cfgs[name]
		if !ok {
			continue
		}
		cacheKey := string(raw) + "|auth:" + strconv.FormatInt(g.mcpAuthEpoch(name), 10)
		if !body.Fresh {
			if res, hit := g.mcpCache.get(cacheKey); hit {
				mu.Lock()
				out[name] = res
				mu.Unlock()
				continue
			}
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		url, _ := m["url"].(string)
		if strings.TrimSpace(url) == "" {
			cmdName, _ := m["command"].(string)
			if strings.TrimSpace(cmdName) == "" {
				continue
			}
			args := []string{}
			if a, ok := m["args"].([]any); ok {
				for _, v := range a {
					args = append(args, fmt.Sprint(v))
				}
			}
			env := map[string]string{}
			if e, ok := m["env"].(map[string]any); ok {
				for k, v := range e {
					env[k] = fmt.Sprint(v)
				}
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(name, cmdName string, args []string, env map[string]string, key string) {
				defer wg.Done()
				defer func() { <-sem }()
				ctx, cancel := context.WithTimeout(r.Context(), mcpStdioTimeout)
				defer cancel()
				res := checkMCPStdio(ctx, cmdName, args, env)
				g.cacheMCPCheck(key, res)
				mu.Lock()
				out[name] = res
				mu.Unlock()
			}(name, cmdName, args, env, cacheKey)
			continue
		}
		if t, _ := m["type"].(string); t == "sse" {
			hdr, missing := expandHeaderMap(m["headers"])
			if len(missing) == 0 && !hasHeader(hdr, "Authorization") {
				if tok, ok := g.mcpBearerFor(name, url); ok {
					hdr["Authorization"] = "Bearer " + tok
				}
			}
			if len(missing) > 0 {
				res := mcpCheckResult{Why: "novar", Detail: strings.Join(missing, ", ")}
				g.cacheMCPCheck(cacheKey, res)
				mu.Lock()
				out[name] = res
				mu.Unlock()
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(name, url string, headers map[string]string, key string) {
				defer wg.Done()
				defer func() { <-sem }()
				ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
				defer cancel()
				res := checkMCPSSE(ctx, url, headers)
				g.cacheMCPCheck(key, res)
				mu.Lock()
				out[name] = res
				mu.Unlock()
			}(name, url, hdr, cacheKey)
			continue
		}
		headers, missing := expandHeaderMap(m["headers"])
		if len(missing) == 0 && !hasHeader(headers, "Authorization") {
			if tok, ok := g.mcpBearerFor(name, url); ok {
				headers["Authorization"] = "Bearer " + tok
			}
		}
		if len(missing) > 0 {
			res := mcpCheckResult{Why: "novar", Detail: strings.Join(missing, ", ")}
			g.cacheMCPCheck(cacheKey, res)
			mu.Lock()
			out[name] = res
			mu.Unlock()
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(name, url string, headers map[string]string, key string) {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
			defer cancel()
			res := checkMCPHTTP(ctx, url, headers)
			g.cacheMCPCheck(key, res)
			mu.Lock()
			out[name] = res
			mu.Unlock()
		}(name, url, headers, cacheKey)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"servers": out})
}

// ---- stdio MCP 服务器检查 ----

// stdio 服务器经 npx/uvx 冷启动常超过 10s，6s 会把正常服务器误判为 timeout。
const mcpStdioTimeout = 25 * time.Second

const (
	mcpInitBody        = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"aether-cockpit","version":"1"}}}`
	mcpInitializedBody = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	mcpToolsBody       = `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
)

// lockedBuffer 收集 stderr（进程仍在写时读取不产生数据竞争）。
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.b.Len() < 8<<10 {
		_, _ = l.b.Write(p)
	}
	return len(p), nil
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func expandMCPRefs(s string) (string, []string) {
	var missing []string
	out := os.Expand(s, func(k string) string {
		v := os.Getenv(k)
		if v == "" {
			missing = append(missing, k)
		}
		return v
	})
	return out, missing
}

func waitMCPRPC(sc *bufio.Scanner, id int) ([]byte, bool) {
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var probe struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(line, &probe) != nil || probe.ID != id {
			continue
		}
		return append([]byte(nil), line...), true
	}
	return nil, false
}

func mcpStdioExchange(stdin io.Writer, stdout io.Reader, stderr *lockedBuffer) mcpCheckResult {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	send := func(body string) bool {
		_, err := io.WriteString(stdin, body+"\n")
		return err == nil
	}
	if !send(mcpInitBody) {
		return mcpCheckResult{Why: "start", Detail: clipText(stderr.String(), 300)}
	}
	if _, ok := waitMCPRPC(sc, 1); !ok {
		return mcpCheckResult{Why: "exited", Detail: clipText(stderr.String(), 300)}
	}
	_ = send(mcpInitializedBody)
	if !send(mcpToolsBody) {
		return mcpCheckResult{Why: "start", Step: "tools/list", Detail: clipText(stderr.String(), 300)}
	}
	line, ok := waitMCPRPC(sc, 2)
	if !ok {
		return mcpCheckResult{Why: "exited", Step: "tools/list", Detail: clipText(stderr.String(), 300)}
	}
	names, ok := parseMCPTools(line)
	if !ok {
		return mcpCheckResult{Why: "protocol", Step: "tools/list", Detail: clipText(string(line), 300)}
	}
	return mcpCheckResult{State: "ok", Tools: len(names), ToolList: names}
}

func checkMCPStdio(ctx context.Context, command string, args []string, env map[string]string) mcpCheckResult {
	start := time.Now()
	var missing []string
	expArgs := make([]string, 0, len(args))
	for _, a := range args {
		v, miss := expandMCPRefs(a)
		expArgs = append(expArgs, v)
		missing = append(missing, miss...)
	}
	envList := os.Environ()
	for k, v := range env {
		ev, miss := expandMCPRefs(v)
		missing = append(missing, miss...)
		envList = append(envList, k+"="+ev)
	}
	if len(missing) > 0 {
		missing = dedupeStrings(missing)
		sort.Strings(missing)
		return mcpCheckResult{Why: "novar", Detail: strings.Join(missing, ", ")}
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return mcpCheckResult{Why: "notfound", Detail: command}
	}
	cmd := exec.CommandContext(ctx, path, expArgs...)
	executil.Hide(cmd)
	cmd.Env = envList
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return mcpCheckResult{Why: "start", Detail: clipText(err.Error(), 300)}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return mcpCheckResult{Why: "start", Detail: clipText(err.Error(), 300)}
	}
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return mcpCheckResult{Why: "start", Detail: clipText(err.Error(), 300)}
	}
	resCh := make(chan mcpCheckResult, 1)
	go func() {
		res := mcpStdioExchange(stdin, stdout, stderr)
		_ = stdin.Close()
		_ = cmd.Wait()
		resCh <- res
	}()
	select {
	case res := <-resCh:
		if ctx.Err() == context.DeadlineExceeded {
			return mcpCheckResult{
				Why: "timeout", Step: "initialize", Detail: fmt.Sprintf("no answer in %s", time.Since(start).Round(time.Second)),
				Waited: time.Since(start).Milliseconds(),
			}
		}
		return res
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-resCh
		return mcpCheckResult{
			Why: "timeout", Step: "initialize", Detail: fmt.Sprintf("no answer in %s", time.Since(start).Round(time.Second)),
			Waited: time.Since(start).Milliseconds(),
		}
	}
}
