package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 旧 SSE 传输：GET 事件流先收到 `event: endpoint`，之后的 JSON-RPC 都
// POST 到该地址，响应从事件流回来（`event: message`）。

// expandHeaderMap 展开 headers 里的 ${VAR}，返回缺失的变量名（排序去重）。
func expandHeaderMap(v any) (map[string]string, []string) {
	h, _ := v.(map[string]any)
	out := map[string]string{}
	missing := []string{}
	for k, x := range h {
		val, miss := expandMCPRefs(fmt.Sprint(x))
		out[k] = val
		missing = append(missing, miss...)
	}
	missing = dedupeStrings(missing)
	sort.Strings(missing)
	return out, missing
}

func readMCPSSE(r io.Reader, endpointCh chan<- string, msgCh chan<- []byte) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var event, data string
	emit := func() {
		switch event {
		case "endpoint":
			if data != "" {
				select {
				case endpointCh <- data:
				default:
				}
			}
		case "message":
			if data != "" {
				select {
				case msgCh <- []byte(data):
				default:
				}
			}
		}
		event, data = "", ""
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			emit()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				data = d
			} else {
				data += "\n" + d
			}
		}
	}
}

// waitSSEStep 等 id 匹配的响应。第三个返回值是期间收到的所有 id（按到达顺序）：
// 失败时把它写进 detail，才能分清"响应没来"和"id 不匹配"。
func waitSSEStep(ctx context.Context, msgCh <-chan []byte, id int) ([]byte, bool, []string) {
	seen := make([]string, 0, 4)
	for {
		select {
		case <-ctx.Done():
			return nil, false, seen
		case msg, ok := <-msgCh:
			if !ok {
				return nil, false, seen
			}
			var probe struct {
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(msg, &probe) == nil && len(probe.ID) > 0 {
				got := string(probe.ID)
				seen = append(seen, got)
				if got == strconv.Itoa(id) {
					return msg, true, seen
				}
			} else {
				seen = append(seen, "(no id)")
			}
		}
	}
}

func checkMCPSSE(ctx context.Context, rawURL string, headers map[string]string) mcpCheckResult {
	start := time.Now()
	client := &http.Client{}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return mcpCheckResult{Why: "unreachable", Detail: clipText(err.Error(), 300)}
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		why, waited := mcpNetWhy(err, time.Since(start))
		return mcpCheckResult{Why: why, Step: "endpoint", Detail: clipText(err.Error(), 300), Waited: waited}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return mcpCheckResult{State: "auth", Code: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return mcpCheckResult{Why: "http", Code: resp.StatusCode, Step: "endpoint"}
	}

	endpointCh := make(chan string, 1)
	msgCh := make(chan []byte, 32)
	go readMCPSSE(resp.Body, endpointCh, msgCh)

	var endpoint string
	select {
	case endpoint = <-endpointCh:
	case <-ctx.Done():
		return mcpCheckResult{Why: "timeout", Step: "endpoint", Detail: "no endpoint event in 6 seconds", Waited: time.Since(start).Milliseconds()}
	}
	base, err := url.Parse(rawURL)
	if err != nil {
		return mcpCheckResult{Why: "protocol", Step: "endpoint", Detail: clipText(err.Error(), 300)}
	}
	ep, err := base.Parse(strings.TrimSpace(endpoint))
	if err != nil || ep.Scheme == "" {
		return mcpCheckResult{Why: "protocol", Step: "endpoint", Detail: clipText(endpoint, 300)}
	}
	postURL := ep.String()

	post := func(body, step string) *mcpCheckResult {
		code, _, raw, err := mcpPost(ctx, client, postURL, headers, body)
		if err != nil {
			why, waited := mcpNetWhy(err, time.Since(start))
			return &mcpCheckResult{Why: why, Step: step, Detail: clipText(err.Error(), 300), Waited: waited}
		}
		if code == http.StatusUnauthorized || code == http.StatusForbidden {
			return &mcpCheckResult{State: "auth", Code: code, Detail: clipText(string(raw), 300)}
		}
		if code < 200 || code >= 300 {
			return &mcpCheckResult{Why: "http", Code: code, Step: step, Detail: clipText(string(raw), 300)}
		}
		return nil
	}

	if bad := post(mcpInitBody, "initialize"); bad != nil {
		return *bad
	}
	if _, ok, seen := waitSSEStep(ctx, msgCh, 1); !ok {
		if ctx.Err() != nil {
			return mcpCheckResult{Why: "timeout", Step: "initialize", Detail: "no answer in 6 seconds; ids seen: " + strings.Join(seen, ","), Waited: time.Since(start).Milliseconds()}
		}
		return mcpCheckResult{Why: "protocol", Step: "initialize", Detail: "stream ended before the answer; ids seen: " + strings.Join(seen, ",")}
	}
	if bad := post(mcpInitializedBody, "initialize"); bad != nil {
		return *bad
	}
	if bad := post(mcpToolsBody, "tools/list"); bad != nil {
		return *bad
	}
	msg, ok, seen := waitSSEStep(ctx, msgCh, 2)
	if !ok {
		if ctx.Err() != nil {
			return mcpCheckResult{Why: "timeout", Step: "tools/list", Detail: "no answer in 6 seconds; ids seen: " + strings.Join(seen, ","), Waited: time.Since(start).Milliseconds()}
		}
		return mcpCheckResult{Why: "protocol", Step: "tools/list", Detail: "stream ended before the answer; ids seen: " + strings.Join(seen, ",")}
	}
	names, ok := parseMCPTools(msg)
	if !ok {
		return mcpCheckResult{Why: "protocol", Step: "tools/list", Detail: clipText(string(msg), 300)}
	}
	return mcpCheckResult{State: "ok", Tools: len(names), ToolList: names}
}
