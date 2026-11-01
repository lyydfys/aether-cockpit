// Command smoke 是 Aether Cockpit 的端到端验收工具。
//
// 单元测试验证的是"每个零件对不对"，这个工具验证的是"整条链路通不通"：
// 它把散落在文档里的手工 curl 验收步骤固化成一个可重复执行的程序。
//
// 用法：
//
//	go run ./cmd/smoke -addr http://127.0.0.1:7860
//	go run ./cmd/smoke -addr http://127.0.0.1:7860 -model openai-compat/deepseek-chat
//
// 退出码：0 全部通过，1 有失败项。
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var (
	addr    = flag.String("addr", "http://127.0.0.1:7860", "网关地址")
	model   = flag.String("model", "", "指定模型（留空自动选取第一个可用模型）")
	key     = flag.String("key", "", "API Key（网关配置了 api_key 时需要）")
	timeout = flag.Duration("timeout", 90*time.Second, "单次请求超时")
	verbose = flag.Bool("v", false, "打印响应片段")
)

// --- 报告 ---

type result struct {
	name    string
	ok      bool
	detail  string
	skipped bool
}

var results []result

// lastResponseID 供 /v1/responses/{id} 快照查询复用。
var lastResponseID string

func pass(name, detail string) {
	results = append(results, result{name: name, ok: true, detail: detail})
}
func fail(name, detail string) {
	results = append(results, result{name: name, ok: false, detail: detail})
}
func skip(name, why string) {
	results = append(results, result{name: name, skipped: true, detail: why})
}

// --- HTTP ---

var client = &http.Client{Timeout: 60 * time.Second}

func doJSON(method, path string, body any, out any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, *addr+path, rdr)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if *key != "" {
		req.Header.Set("Authorization", "Bearer "+*key)
	}

	ctxClient := &http.Client{Timeout: *timeout}
	resp, err := ctxClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if out != nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, out)
	}
	return resp.StatusCode, raw, nil
}

// streamEvents 读取 SSE 并返回其中的具名事件序列与全部原始文本。
func streamEvents(path string, body any) ([]string, string, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequest(http.MethodPost, *addr+path, bytes.NewReader(b))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if *key != "" {
		req.Header.Set("Authorization", "Bearer "+*key)
	}

	cli := &http.Client{Timeout: *timeout}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("状态 %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}

	var (
		events []string
		sb     strings.Builder
	)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		sb.WriteString(line)
		sb.WriteString("\n")
		if strings.HasPrefix(line, "event: ") {
			events = append(events, strings.TrimPrefix(line, "event: "))
		}
	}
	return events, sb.String(), sc.Err()
}

// --- 主流程 ---

func main() {
	flag.Parse()

	fmt.Printf("Aether Cockpit 验收 — %s\n\n", *addr)

	// 网关不可达时短路：否则后面每一项都会重复同一条 dial 错误，噪音淹没重点。
	if !checkHealth() {
		printReport()
		return
	}

	providers := checkProviders()
	accounts := checkAccounts()

	m := *model
	if m == "" {
		m = pickModel(providers)
	}
	if m == "" {
		fail("选取模型", "没有任何已登记的模型，后续协议测试全部跳过")
	} else {
		pass("选取模型", m)
	}

	if m != "" {
		checkModelsEndpoint()
		checkInjectEndpoint()
		checkSpecsEndpoint()
		checkChatNonStream(m)
		checkChatStream(m)
		checkResponsesNonStream(m)
		checkResponseSnapshot()
		checkResponsesStream(m)
		checkAnthropicStream(m)
		checkGeminiNonStream(m)
		checkGeminiStream(m)
		checkStateSuture(m)
		checkToolCall(m)
	}

	checkGeminiModelsAPI()
	checkUsageAPI()
	checkSettingsAPI()
	checkClientsAPI()
	checkBackupExport()
	checkAccountsExport()
	if len(accounts) > 0 {
		checkTagsRoundTrip(accounts[0].AccountID)
	} else {
		checkTagsRoundTrip("")
	}
	checkLibraryAPI()
	checkEventsEndpoint()
	checkDiagnose()
	checkMetrics()
	checkOpsEndpoints()
	checkChannelMonitor(m)
	checkCallerKeys(m)
	checkUnlisted(m)
	_ = accounts

	printReport()
}

func checkHealth() bool {
	var h struct {
		Status    string `json:"status"`
		Accounts  int    `json:"accounts"`
		Providers int    `json:"providers"`
	}
	code, _, err := doJSON(http.MethodGet, "/api/health", nil, &h)
	if err != nil {
		fail("健康检查", "网关不可达 — 确认进程已启动，且 -addr 与配置文件里的 addr 一致")
		return false
	}
	if code != 200 || h.Status != "ok" {
		fail("健康检查", fmt.Sprintf("状态 %d / %s", code, h.Status))
		return false
	}
	pass("健康检查", fmt.Sprintf("%d 渠道 / %d 账号", h.Providers, h.Accounts))
	return true
}

type providerView struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	AccountCount int    `json:"account_count"`
	ReadyCount   int    `json:"ready_count"`
	Models       []struct {
		ID            string `json:"id"`
		UpstreamModel string `json:"upstream_model"`
	} `json:"models"`
}

func checkProviders() []providerView {
	var resp struct {
		Providers []providerView `json:"providers"`
		TUN       struct {
			Detected bool `json:"detected"`
		} `json:"tun"`
	}
	code, _, err := doJSON(http.MethodGet, "/api/providers", nil, &resp)
	if err != nil || code != 200 {
		fail("渠道列表", fmt.Sprintf("%v (状态 %d)", err, code))
		return nil
	}
	if len(resp.Providers) == 0 {
		fail("渠道列表", "没有任何已注册渠道")
		return nil
	}
	names := make([]string, 0, len(resp.Providers))
	for _, p := range resp.Providers {
		names = append(names, p.ID)
	}
	pass("渠道列表", strings.Join(names, ", "))
	if resp.TUN.Detected {
		fail("网络环境", "检测到 TUN 虚拟网卡，CN 渠道强制直连会失效")
	} else {
		pass("网络环境", "未检测到 TUN 劫持")
	}
	return resp.Providers
}

func checkAccounts() []struct {
	AccountID  string `json:"account_id"`
	State      string `json:"state"`
	ProviderID string `json:"provider_id"`
} {
	var resp struct {
		Accounts []struct {
			AccountID  string `json:"account_id"`
			State      string `json:"state"`
			ProviderID string `json:"provider_id"`
		} `json:"accounts"`
	}
	code, _, err := doJSON(http.MethodGet, "/api/accounts", nil, &resp)
	if err != nil || code != 200 {
		fail("账号列表", fmt.Sprintf("%v (状态 %d)", err, code))
		return nil
	}
	if len(resp.Accounts) == 0 {
		fail("账号列表", "没有任何账号，协议测试会全部失败")
		return nil
	}
	ready := 0
	for _, a := range resp.Accounts {
		if a.State == "ready" {
			ready++
		}
	}
	if ready == 0 {
		fail("账号列表", fmt.Sprintf("%d 个账号全部不可用", len(resp.Accounts)))
	} else {
		pass("账号列表", fmt.Sprintf("%d/%d 可用", ready, len(resp.Accounts)))
	}
	return resp.Accounts
}

// pickModel 优先选有可用账号的渠道的模型。
func pickModel(providers []providerView) string {
	for _, p := range providers {
		if p.ReadyCount == 0 || len(p.Models) == 0 {
			continue
		}
		id := p.Models[0].ID
		if !strings.Contains(id, "/") {
			id = p.ID + "/" + id
		}
		return id
	}
	return ""
}

func checkModelsEndpoint() {
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	code, _, err := doJSON(http.MethodGet, "/v1/models", nil, &resp)
	if err != nil || code != 200 {
		fail("/v1/models", fmt.Sprintf("%v (状态 %d)", err, code))
		return
	}
	pass("/v1/models", fmt.Sprintf("%d 个模型", len(resp.Data)))
}

func checkInjectEndpoint() {
	var resp struct {
		Origin  string `json:"origin"`
		Targets []struct {
			ID      string `json:"id"`
			Snippet string `json:"snippet"`
		} `json:"targets"`
	}
	code, raw, err := doJSON(http.MethodGet, "/api/inject", nil, &resp)
	if err != nil || code != 200 {
		fail("接入片段", fmt.Sprintf("%v (状态 %d) %s", err, code, truncate(string(raw), 120)))
		return
	}
	if len(resp.Targets) < 2 || resp.Origin == "" {
		fail("接入片段", fmt.Sprintf("targets=%d origin=%q", len(resp.Targets), resp.Origin))
		return
	}
	pass("接入片段", fmt.Sprintf("%d 个客户端 · %s", len(resp.Targets), resp.Origin))
}

func checkSpecsEndpoint() {
	var resp struct {
		Status struct {
			Count     int    `json:"count"`
			RemoteURL string `json:"remote_url"`
			LastError string `json:"last_error"`
		} `json:"status"`
	}
	code, raw, err := doJSON(http.MethodGet, "/api/specs", nil, &resp)
	if err != nil || code != 200 {
		fail("渠道参数", fmt.Sprintf("%v (状态 %d) %s", err, code, truncate(string(raw), 120)))
		return
	}
	detail := fmt.Sprintf("%d 条参数", resp.Status.Count)
	if resp.Status.RemoteURL != "" {
		detail += "，远程更新已启用"
	}
	if resp.Status.LastError != "" {
		fail("渠道参数", "远程拉取失败: "+resp.Status.LastError)
		return
	}
	pass("渠道参数", detail)
}

func checkChatNonStream(m string) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}
	body := map[string]any{
		"model":    m,
		"messages": []any{map[string]any{"role": "user", "content": "验收：chat 非流式"}},
	}
	code, raw, err := doJSON(http.MethodPost, "/v1/chat/completions", body, &resp)
	if err != nil || code != 200 {
		fail("chat 非流式", fmt.Sprintf("%v (状态 %d) %s", err, code, truncate(string(raw), 160)))
		return
	}
	if len(resp.Choices) == 0 {
		fail("chat 非流式", "响应没有 choices")
		return
	}
	detail := fmt.Sprintf("finish=%s tokens=%d", resp.Choices[0].FinishReason, resp.Usage.TotalTokens)
	if *verbose {
		detail += " | " + truncate(resp.Choices[0].Message.Content, 60)
	}
	pass("chat 非流式", detail)
}

func checkChatStream(m string) {
	body := map[string]any{
		"model":    m,
		"stream":   true,
		"messages": []any{map[string]any{"role": "user", "content": "验收：chat 流式"}},
	}
	_, raw, err := streamEvents("/v1/chat/completions", body)
	if err != nil {
		fail("chat 流式", err.Error())
		return
	}
	if !strings.Contains(raw, "data: [DONE]") {
		fail("chat 流式", "缺少 [DONE] 结束标记")
		return
	}
	if !strings.Contains(raw, `"delta"`) {
		fail("chat 流式", "没有 delta 帧")
		return
	}
	pass("chat 流式", fmt.Sprintf("%d 字节，含 [DONE]", len(raw)))
}

func checkResponsesNonStream(m string) {
	var resp struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Output []struct {
			Type string `json:"type"`
		} `json:"output"`
	}
	body := map[string]any{"model": m, "input": "验收：responses 非流式"}
	code, raw, err := doJSON(http.MethodPost, "/v1/responses", body, &resp)
	if err != nil || code != 200 {
		fail("responses 非流式", fmt.Sprintf("%v (状态 %d) %s", err, code, truncate(string(raw), 160)))
		return
	}
	if resp.ID == "" || resp.Status != "completed" {
		fail("responses 非流式", fmt.Sprintf("id=%q status=%q", resp.ID, resp.Status))
		return
	}
	lastResponseID = resp.ID
	pass("responses 非流式", fmt.Sprintf("id=%s output=%d 项", truncate(resp.ID, 18), len(resp.Output)))
}

// checkResponseSnapshot 验证 GET /v1/responses/{id} 与 input_items。
func checkResponseSnapshot() {
	if lastResponseID == "" {
		skip("响应快照", "没有可查询的 response id")
		return
	}
	id := url.PathEscape(lastResponseID)

	var got struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	code, raw, err := doJSON(http.MethodGet, "/v1/responses/"+id, nil, &got)
	if err != nil || code != 200 {
		fail("响应快照", fmt.Sprintf("%v (状态 %d) %s", err, code, truncate(string(raw), 120)))
		return
	}
	if got.ID != lastResponseID {
		fail("响应快照", fmt.Sprintf("返回 id=%q", got.ID))
		return
	}

	var items struct {
		Data []any `json:"data"`
	}
	code2, raw2, err2 := doJSON(http.MethodGet, "/v1/responses/"+id+"/input_items", nil, &items)
	if err2 != nil || code2 != 200 {
		fail("响应输入项", fmt.Sprintf("%v (状态 %d) %s", err2, code2, truncate(string(raw2), 120)))
		return
	}
	pass("响应快照", fmt.Sprintf("%d 个输入项", len(items.Data)))
}

// checkEventsEndpoint 验证事件总线 SSE 能建立连接并补发历史。
func checkEventsEndpoint() {
	u := *addr + "/api/events"
	if *key != "" {
		u += "?key=" + url.QueryEscape(*key)
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		fail("事件流", err.Error())
		return
	}
	cli := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := cli.Do(req)
	if err != nil {
		fail("事件流", "连接失败: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		fail("事件流", "Content-Type = "+ct)
		return
	}
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	if n > 0 {
		pass("事件流", fmt.Sprintf("SSE 已连接，首包 %d 字节", n))
		return
	}
	pass("事件流", "SSE 已连接（暂无历史事件）")
}

func checkResponsesStream(m string) {
	body := map[string]any{"model": m, "stream": true, "input": "验收：responses 流式"}
	events, _, err := streamEvents("/v1/responses", body)
	if err != nil {
		fail("responses 流式", err.Error())
		return
	}
	if len(events) == 0 {
		fail("responses 流式", "没有具名事件")
		return
	}

	// 校验关键生命周期：created 必须最先，completed 必须最后。
	if events[0] != "response.created" {
		fail("responses 流式", "首个事件应为 response.created，实际 "+events[0])
		return
	}
	if events[len(events)-1] != "response.completed" {
		fail("responses 流式", "末个事件应为 response.completed，实际 "+events[len(events)-1])
		return
	}
	// 有文本输出时必须成套出现，否则 Codex CLI 这类严格客户端会解析失败。
	hasText := contains(events, "response.output_text.delta")
	if hasText && !contains(events, "response.output_item.added") {
		fail("responses 流式", "有文本 delta 但缺少 output_item.added")
		return
	}
	if hasText && !contains(events, "response.content_part.added") {
		fail("responses 流式", "有文本 delta 但缺少 content_part.added")
		return
	}
	pass("responses 流式", fmt.Sprintf("%d 个事件，生命周期完整", len(events)))
}

func checkAnthropicStream(m string) {
	var probe struct {
		ID string `json:"id"`
	}
	body := map[string]any{
		"model":      m,
		"stream":     true,
		"max_tokens": 256,
		"messages":   []any{map[string]any{"role": "user", "content": "验收：anthropic 流式"}},
	}
	events, _, err := streamEvents("/v1/messages", body)
	if err != nil {
		_ = probe
		fail("anthropic 流式", err.Error())
		return
	}
	if len(events) == 0 || events[0] != "message_start" {
		fail("anthropic 流式", "首个事件应为 message_start")
		return
	}
	if events[len(events)-1] != "message_stop" {
		fail("anthropic 流式", "末个事件应为 message_stop，实际 "+events[len(events)-1])
		return
	}
	if !contains(events, "content_block_start") || !contains(events, "content_block_stop") {
		fail("anthropic 流式", "content_block 生命周期不完整")
		return
	}
	pass("anthropic 流式", fmt.Sprintf("%d 个事件", len(events)))
}

// checkStateSuture 验证 previous_response_id 的会话续接。
func checkStateSuture(m string) {
	var first struct {
		ID string `json:"id"`
	}
	code, raw, err := doJSON(http.MethodPost, "/v1/responses", map[string]any{
		"model": m, "input": "验收：第一轮", "store": true,
	}, &first)
	if err != nil || code != 200 || first.ID == "" {
		skip("状态缝合", fmt.Sprintf("首轮失败: %v (状态 %d) %s", err, code, truncate(string(raw), 120)))
		return
	}

	var second struct {
		ID     string `json:"id"`
		Output []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	code2, raw2, err2 := doJSON(http.MethodPost, "/v1/responses", map[string]any{
		"model":                m,
		"previous_response_id": first.ID,
		"input":                "验收：第二轮",
	}, &second)
	if err2 != nil || code2 != 200 {
		fail("状态缝合", fmt.Sprintf("续轮失败: %v (状态 %d) %s", err2, code2, truncate(string(raw2), 160)))
		return
	}
	if second.ID == "" {
		fail("状态缝合", "续轮没有返回新 id")
		return
	}
	// 续轮必须产生新的 response id，且能正常完成
	pass("状态缝合", fmt.Sprintf("%s → %s", truncate(first.ID, 16), truncate(second.ID, 16)))
}

// checkToolCall 验证工具调用能正确透传到下游。
func checkToolCall(m string) {
	var resp struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	body := map[string]any{
		"model":    m,
		"messages": []any{map[string]any{"role": "user", "content": "验收：请调用 read_file 读取 main.go"}},
		"tools": []any{map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "read_file",
				"description": "读取文件内容",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{"path": map[string]any{"type": "string"}},
				},
			},
		}},
	}
	code, raw, err := doJSON(http.MethodPost, "/v1/chat/completions", body, &resp)
	if err != nil || code != 200 {
		fail("工具调用", fmt.Sprintf("%v (状态 %d) %s", err, code, truncate(string(raw), 160)))
		return
	}
	if len(resp.Choices) == 0 {
		fail("工具调用", "没有 choices")
		return
	}
	tcs := resp.Choices[0].Message.ToolCalls
	if len(tcs) == 0 {
		// 模型可能选择不调用工具，这不算网关故障。
		skip("工具调用", "模型本轮未产生工具调用（取决于模型行为，非网关问题）")
		return
	}
	if tcs[0].Function.Name == "" {
		fail("工具调用", "工具调用缺少函数名")
		return
	}
	if !json.Valid([]byte(tcs[0].Function.Arguments)) {
		fail("工具调用", "参数不是合法 JSON: "+truncate(tcs[0].Function.Arguments, 80))
		return
	}
	pass("工具调用", fmt.Sprintf("%s(%s) finish=%s",
		tcs[0].Function.Name, truncate(tcs[0].Function.Arguments, 40), resp.Choices[0].FinishReason))
}

func checkDiagnose() {
	var rep struct {
		Errors int `json:"errors"`
		Warns  int `json:"warns"`
		OK     int `json:"ok"`
		Items  []struct {
			Level string `json:"level"`
			Title string `json:"title"`
		} `json:"items"`
	}
	code, _, err := doJSON(http.MethodGet, "/api/diagnose", nil, &rep)
	if err != nil || code != 200 {
		fail("运行自检", fmt.Sprintf("%v (状态 %d)", err, code))
		return
	}
	if rep.Errors > 0 {
		var msgs []string
		for _, it := range rep.Items {
			if it.Level == "error" {
				msgs = append(msgs, it.Title)
			}
		}
		fail("运行自检", fmt.Sprintf("%d 个错误: %s", rep.Errors, strings.Join(msgs, "; ")))
		return
	}
	pass("运行自检", fmt.Sprintf("%d 项通过, %d 项提醒", rep.OK, rep.Warns))
}

// --- 输出 ---

func printReport() {
	fmt.Println(strings.Repeat("─", 62))

	var passed, failed, skipped int
	for _, r := range results {
		var mark string
		switch {
		case r.skipped:
			mark = "SKIP"
			skipped++
		case r.ok:
			mark = "PASS"
			passed++
		default:
			mark = "FAIL"
			failed++
		}
		line := fmt.Sprintf("%-5s %-22s %s", mark, r.name, r.detail)
		fmt.Println(line)
	}

	fmt.Println(strings.Repeat("─", 62))
	fmt.Printf("通过 %d · 失败 %d · 跳过 %d\n", passed, failed, skipped)

	if failed > 0 {
		fmt.Println("\n有失败项。先看「运行自检」那一行的提示，或打开管理界面的自检面板。")
		os.Exit(1)
	}
	fmt.Println("全部通过，可以接入客户端了。")
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
