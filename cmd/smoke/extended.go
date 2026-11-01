package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 扩展验收 —— 覆盖对齐 magpie / cockpit-tools 后新增的能力：
// Gemini 协议、用量统计、运行时设置、客户端注入元信息、备份导出、账号标签、
// 库（prompts / MCP / skills）的增删查。
//
// 与 main.go 里的基础验收共用 pass/fail/skip 与 doJSON。
// 刻意只做"读 + 可还原的写"：不应用客户端注入（会改用户的配置文件）、
// 不改运行时设置（会覆盖用户已保存的隐私开关）。

// checkGeminiNonStream 验证 /v1beta/models/{m}:generateContent。
func checkGeminiNonStream(m string) {
	var resp struct {
		Candidates []struct {
			Content struct {
				Role  string `json:"role"`
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata map[string]int `json:"usageMetadata"`
	}
	body := map[string]any{
		"contents": []any{map[string]any{
			"role":  "user",
			"parts": []any{map[string]any{"text": "验收：gemini 非流式"}},
		}},
	}
	code, raw, err := doJSON(http.MethodPost, "/v1beta/models/"+m+":generateContent", body, &resp)
	if err != nil || code != 200 {
		fail("gemini 非流式", fmt.Sprintf("%v (状态 %d) %s", err, code, truncate(string(raw), 160)))
		return
	}
	if len(resp.Candidates) == 0 {
		fail("gemini 非流式", "没有 candidates")
		return
	}
	c := resp.Candidates[0]
	if c.Content.Role != "model" || c.FinishReason == "" {
		fail("gemini 非流式", fmt.Sprintf("role=%q finishReason=%q", c.Content.Role, c.FinishReason))
		return
	}
	pass("gemini 非流式", fmt.Sprintf("finishReason=%s parts=%d", c.FinishReason, len(c.Content.Parts)))
}

// checkGeminiStream 验证 :streamGenerateContent?alt=sse。
func checkGeminiStream(m string) {
	_, raw, err := postRaw("/v1beta/models/"+m+":streamGenerateContent?alt=sse", map[string]any{
		"contents": []any{map[string]any{
			"role":  "user",
			"parts": []any{map[string]any{"text": "验收：gemini 流式"}},
		}},
	})
	if err != nil {
		fail("gemini 流式", err.Error())
		return
	}
	frames := sseDataFrames(raw)
	if len(frames) == 0 {
		fail("gemini 流式", "没有收到 SSE data 帧")
		return
	}
	hasFinish, hasUsage := false, false
	for _, f := range frames {
		if strings.Contains(f, "finishReason") {
			hasFinish = true
		}
		if strings.Contains(f, "usageMetadata") {
			hasUsage = true
		}
	}
	if !hasFinish || !hasUsage {
		fail("gemini 流式", fmt.Sprintf("finishReason=%v usageMetadata=%v", hasFinish, hasUsage))
		return
	}
	pass("gemini 流式", fmt.Sprintf("%d 帧", len(frames)))
}

// checkGeminiModelsAPI 验证 Gemini 格式的模型清单与 countTokens。
func checkGeminiModelsAPI() {
	var list struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	code, _, err := doJSON(http.MethodGet, "/v1beta/models", nil, &list)
	if err != nil || code != 200 || len(list.Models) == 0 {
		fail("gemini 模型清单", fmt.Sprintf("%v (状态 %d, %d 条)", err, code, len(list.Models)))
		return
	}

	var cnt struct {
		TotalTokens int `json:"totalTokens"`
	}
	code2, _, err2 := doJSON(http.MethodPost, "/v1beta/models/"+list.Models[0].Name+":countTokens", map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hello world"}}}},
	}, &cnt)
	if err2 != nil || code2 != 200 || cnt.TotalTokens < 1 {
		fail("gemini 模型清单", fmt.Sprintf("countTokens: %v (状态 %d)", err2, code2))
		return
	}
	pass("gemini 模型清单", fmt.Sprintf("%d 个模型, countTokens=%d", len(list.Models), cnt.TotalTokens))
}

// checkUsageAPI 验证用量统计端点。
func checkUsageAPI() {
	var resp struct {
		Totals struct {
			Requests int64 `json:"requests"`
		} `json:"totals"`
		Days []struct {
			Date string `json:"date"`
		} `json:"days"`
	}
	code, _, err := doJSON(http.MethodGet, "/api/usage?days=7", nil, &resp)
	if err != nil || code != 200 {
		fail("用量统计", fmt.Sprintf("%v (状态 %d)", err, code))
		return
	}
	if len(resp.Days) != 7 {
		fail("用量统计", fmt.Sprintf("返回 %d 天, want 7", len(resp.Days)))
		return
	}
	pass("用量统计", fmt.Sprintf("7 天窗口, 累计 %d 请求", resp.Totals.Requests))
}

// checkSettingsAPI 验证运行时设置端点（只读）。
func checkSettingsAPI() {
	var resp struct {
		Settings struct {
			PrivacyMask struct {
				Enabled bool `json:"enabled"`
			} `json:"privacy_mask"`
		} `json:"settings"`
	}
	code, _, err := doJSON(http.MethodGet, "/api/settings", nil, &resp)
	if err != nil || code != 200 {
		fail("运行时设置", fmt.Sprintf("%v (状态 %d)", err, code))
		return
	}
	pass("运行时设置", fmt.Sprintf("隐私遮蔽=%v", resp.Settings.PrivacyMask.Enabled))
}

// checkClientsAPI 验证客户端注入元信息端点（只读，不实际改写配置文件）。
func checkClientsAPI() {
	var resp struct {
		Available bool `json:"available"`
		Clients   []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Detected bool   `json:"detected"`
		} `json:"clients"`
	}
	code, _, err := doJSON(http.MethodGet, "/api/clients", nil, &resp)
	if err != nil || code != 200 {
		fail("客户端注入", fmt.Sprintf("%v (状态 %d)", err, code))
		return
	}
	if !resp.Available || len(resp.Clients) < 3 {
		fail("客户端注入", fmt.Sprintf("available=%v clients=%d", resp.Available, len(resp.Clients)))
		return
	}
	detected := 0
	for _, c := range resp.Clients {
		if c.Detected {
			detected++
		}
	}
	pass("客户端注入", fmt.Sprintf("%d 个客户端定义, 本机检测到 %d 个", len(resp.Clients), detected))
}

// checkBackupExport 验证备份导出：可解析、无凭证字段。
func checkBackupExport() {
	var resp map[string]any
	code, raw, err := doJSON(http.MethodGet, "/api/backup/export", nil, &resp)
	if err != nil || code != 200 {
		fail("配置备份", fmt.Sprintf("%v (状态 %d)", err, code))
		return
	}
	if _, ok := resp["version"]; !ok {
		fail("配置备份", "缺少 version 字段")
		return
	}
	// 安全断言：备份文件里不允许出现凭证键名。
	if strings.Contains(string(raw), "credentials_enc") || strings.Contains(string(raw), "api_key\":") {
		fail("配置备份", "备份中出现了凭证字段！")
		return
	}
	specsN := 0
	if arr, ok := resp["specs"].([]any); ok {
		specsN = len(arr)
	}
	pass("配置备份", fmt.Sprintf("version=%v specs=%d，无凭证字段", resp["version"], specsN))
}

// checkAccountsExport 验证脱敏导出。
func checkAccountsExport() {
	var resp struct {
		Note     string `json:"note"`
		Accounts []struct {
			AccountID string `json:"account_id"`
		} `json:"accounts"`
	}
	code, raw, err := doJSON(http.MethodGet, "/api/accounts/export", nil, &resp)
	if err != nil || code != 200 {
		fail("账号脱敏导出", fmt.Sprintf("%v (状态 %d)", err, code))
		return
	}
	if strings.Contains(string(raw), "credentials") {
		fail("账号脱敏导出", "导出中出现 credentials 字段！")
		return
	}
	pass("账号脱敏导出", fmt.Sprintf("%d 个账号，%s", len(resp.Accounts), truncate(resp.Note, 30)))
}

// checkTagsRoundTrip 验证账号标签设置与清除（可还原的写）。
func checkTagsRoundTrip(accountID string) {
	if accountID == "" {
		skip("账号标签", "没有账号")
		return
	}
	var resp struct {
		Tags []string `json:"tags"`
	}
	code, _, err := doJSON(http.MethodPost, "/api/accounts/"+accountID+"/tags", map[string]any{
		"tags": []string{"smoke-test"},
	}, &resp)
	if err != nil || code != 200 || len(resp.Tags) != 1 {
		fail("账号标签", fmt.Sprintf("%v (状态 %d)", err, code))
		return
	}
	// 还原为空。
	var cleared struct {
		Tags []string `json:"tags"`
	}
	_, _, _ = doJSON(http.MethodPost, "/api/accounts/"+accountID+"/tags", map[string]any{
		"tags": []string{},
	}, &cleared)
	pass("账号标签", "设置/清除往返成功")
}

// checkLibraryAPI 验证库 prompts 的保存/查询/删除往返（可还原的写）。
//
// 刻意绝不调用 apply 端点：那会把内容写进用户真实的客户端配置文件
// （~/.claude/CLAUDE.md 等），smoke 无权这样做。
func checkLibraryAPI() {
	// 1. 保存。
	var saved struct {
		Entry struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"entry"`
	}
	code, raw, err := doJSON(http.MethodPost, "/api/library/prompts", map[string]any{
		"name":    "smoke-temp",
		"content": "test",
	}, &saved)
	if err != nil || code != 200 || saved.Entry.ID == "" {
		fail("库 prompts", fmt.Sprintf("保存: %v (状态 %d) %s", err, code, truncate(string(raw), 120)))
		return
	}
	cleanup := func() {
		_, _, _ = doJSON(http.MethodDelete, "/api/library/prompts/"+saved.Entry.ID, nil, nil)
	}

	// 2. 查询应存在。
	var list struct {
		Prompts []struct {
			ID string `json:"id"`
		} `json:"prompts"`
	}
	code, _, err = doJSON(http.MethodGet, "/api/library", nil, &list)
	if err != nil || code != 200 {
		fail("库 prompts", fmt.Sprintf("查询: %v (状态 %d)", err, code))
		cleanup()
		return
	}
	if !containsID(list.Prompts, saved.Entry.ID) {
		fail("库 prompts", fmt.Sprintf("保存后列表中未找到 %q", saved.Entry.ID))
		cleanup()
		return
	}

	// 3. 删除。
	var del struct {
		Deleted bool `json:"deleted"`
	}
	code, _, err = doJSON(http.MethodDelete, "/api/library/prompts/"+saved.Entry.ID, nil, &del)
	if err != nil || code != 200 || !del.Deleted {
		fail("库 prompts", fmt.Sprintf("删除: %v (状态 %d)", err, code))
		return
	}

	// 4. 再查询应不存在。
	code, _, err = doJSON(http.MethodGet, "/api/library", nil, &list)
	if err != nil || code != 200 {
		fail("库 prompts", fmt.Sprintf("删除后查询: %v (状态 %d)", err, code))
		return
	}
	if containsID(list.Prompts, saved.Entry.ID) {
		fail("库 prompts", "删除后列表中仍存在")
		return
	}
	pass("库 prompts", "保存/查询/删除往返成功（未触碰 apply）")
}

// containsID 判断条目列表里是否有指定 ID。
func containsID(items []struct {
	ID string `json:"id"`
}, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// postRaw 发 POST 并返回响应体全文。
func postRaw(path string, body any) (int, string, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequest(http.MethodPost, *addr+path, bytes.NewReader(b))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if *key != "" {
		req.Header.Set("Authorization", "Bearer "+*key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), err
}

// sseDataFrames 从 SSE 文本中抽取 data 帧。
func sseDataFrames(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "data: ") {
			out = append(out, strings.TrimPrefix(line, "data: "))
		}
	}
	return out
}

// --- 第 254~255 轮新增能力的 smoke 覆盖（只在 mock 渠道上跑，避免花真实额度） ---

func providerOfModel(model string) string {
	if i := strings.Index(model, "/"); i > 0 {
		return model[:i]
	}
	return ""
}

// checkMetrics GET /metrics 必须是 Prometheus 文本且含核心指标。
func checkMetrics() {
	resp, err := (&http.Client{Timeout: *timeout}).Get(*addr + "/metrics")
	if err != nil {
		fail("Prometheus 指标", err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if resp.StatusCode != 200 || !strings.Contains(body, "aether_requests_total") ||
		!strings.Contains(body, "aether_uptime_seconds") || !strings.Contains(resp.Header.Get("Content-Type"), "text/plain") {
		fail("Prometheus 指标", fmt.Sprintf("状态 %d / 内容不完整", resp.StatusCode))
		return
	}
	pass("Prometheus 指标", "含 requests/uptime 等指标")
}

// checkChannelMonitor 只在 mock 渠道探一次（真实渠道会消耗额度）。
func checkChannelMonitor(model string) {
	pid := providerOfModel(model)
	if !strings.HasPrefix(pid, "mock") {
		skip("渠道监控探测", "只在 mock 渠道上探测，避免消耗真实额度")
		return
	}
	if code, _, err := doJSON(http.MethodPost, "/api/channels/check", map[string]any{"provider": pid}, nil); err != nil || code != 200 {
		fail("渠道监控探测", fmt.Sprintf("%v (状态 %d)", err, code))
		return
	}
	var status struct {
		Entries []struct {
			Provider string `json:"provider"`
			OK       bool   `json:"ok"`
			Ms       int64  `json:"ms"`
			Error    string `json:"error"`
		} `json:"entries"`
		Reading bool `json:"reading"`
	}
	for i := 0; i < 40; i++ {
		if code, _, err := doJSON(http.MethodGet, "/api/channels/status", nil, &status); err != nil || code != 200 {
			fail("渠道监控探测", fmt.Sprintf("%v (状态 %d)", err, code))
			return
		}
		if !status.Reading {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	for _, e := range status.Entries {
		if e.Provider == pid {
			if !e.OK {
				fail("渠道监控探测", "探测未通过: "+e.Error)
				return
			}
			pass("渠道监控探测", fmt.Sprintf("%s 正常 %d ms", pid, e.Ms))
			return
		}
	}
	fail("渠道监控探测", "没有该渠道的探测结果")
}

// checkCallerKeys 生成一条调用方密钥、用它发请求、加 RPM 限额后应被拒、最后删掉。
func checkCallerKeys(model string) {
	pid := providerOfModel(model)
	if !strings.HasPrefix(pid, "mock") {
		skip("调用方密钥", "只在 mock 模型上验证，避免消耗真实额度")
		return
	}
	name := fmt.Sprintf("smoke-%d", time.Now().UnixNano())
	var added struct {
		Keys []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"keys"`
		Secret string `json:"secret"`
	}
	if code, _, err := doJSON(http.MethodPost, "/api/caller-keys/add-key", map[string]any{"name": name}, &added); err != nil || code != 200 || added.Secret == "" {
		fail("调用方密钥", fmt.Sprintf("创建失败: %v (状态 %d)", err, code))
		return
	}
	id := ""
	for _, k := range added.Keys {
		if k.Name == name {
			id = k.ID
		}
	}
	if id == "" {
		fail("调用方密钥", "创建后列表里没有该密钥")
		return
	}
	defer doJSON(http.MethodPost, "/api/caller-keys/remove", map[string]any{"key": id}, nil)

	chat := map[string]any{
		"model":    model,
		"stream":   false,
		"messages": []map[string]any{{"role": "user", "content": "smoke caller key"}},
	}
	if code, raw, err := doWithKey(http.MethodPost, "/v1/chat/completions", added.Secret, chat); err != nil || code != 200 {
		fail("调用方密钥", fmt.Sprintf("密钥请求失败: %v (状态 %d) %s", err, code, raw))
		return
	}
	if code, _, err := doJSON(http.MethodPost, "/api/caller-keys/limit-key", map[string]any{
		"key": id, "limit": map[string]any{"rpm": 1},
	}, nil); err != nil || code != 200 {
		fail("调用方密钥", fmt.Sprintf("设置限额失败: %v (状态 %d)", err, code))
		return
	}
	// RPM=1 的令牌桶从限额生效时起算：先放行一次，紧接着的下一次必须 429。
	if code, _, err := doWithKey(http.MethodPost, "/v1/chat/completions", added.Secret, chat); err != nil || code != 200 {
		fail("调用方密钥", fmt.Sprintf("限额内首次请求应 200，得到 %d (%v)", code, err))
		return
	}
	code, _, err := doWithKey(http.MethodPost, "/v1/chat/completions", added.Secret, chat)
	if err != nil || code != http.StatusTooManyRequests {
		fail("调用方密钥", fmt.Sprintf("超过 RPM 应 429，得到 %d (%v)", code, err))
		return
	}
	pass("调用方密钥", "生成→可用→限额生效(429)→清理")
}

// checkUnlisted 只在 mock 渠道上切换 unlisted：模型清单隐藏，显式请求仍可用，最后恢复。
func checkUnlisted(model string) {
	pid := providerOfModel(model)
	if !strings.HasPrefix(pid, "mock") {
		skip("unlisted 往返", "只在 mock 渠道上切换，避免影响真实渠道")
		return
	}
	var prov struct {
		Providers []struct {
			ID       string `json:"id"`
			Unlisted bool   `json:"unlisted"`
		} `json:"providers"`
	}
	if code, _, err := doJSON(http.MethodGet, "/api/providers", nil, &prov); err != nil || code != 200 {
		fail("unlisted 往返", fmt.Sprintf("%v (状态 %d)", err, code))
		return
	}
	for _, p := range prov.Providers {
		if p.ID == pid && p.Unlisted {
			skip("unlisted 往返", "该渠道本来就是 unlisted，跳过以免误改")
			return
		}
	}
	if code, _, err := doJSON(http.MethodPost, "/api/provider/save", map[string]any{"id": pid, "unlisted": true}, nil); err != nil || code != 200 {
		fail("unlisted 往返", fmt.Sprintf("设置失败: %v (状态 %d)", err, code))
		return
	}
	defer doJSON(http.MethodPost, "/api/provider/save", map[string]any{"id": pid, "unlisted": false}, nil)

	var models struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if code, _, err := doJSON(http.MethodGet, "/v1/models", nil, &models); err != nil || code != 200 {
		fail("unlisted 往返", fmt.Sprintf("模型清单读取失败: %v", err))
		return
	}
	for _, m := range models.Data {
		if m.ID == model {
			fail("unlisted 往返", "unlisted 后模型仍出现在 /v1/models")
			return
		}
	}
	chat := map[string]any{
		"model":    model,
		"stream":   false,
		"messages": []map[string]any{{"role": "user", "content": "smoke unlisted"}},
	}
	if code, raw, err := doJSON(http.MethodPost, "/v1/chat/completions", chat, nil); err != nil || code != 200 {
		fail("unlisted 往返", fmt.Sprintf("显式请求应仍可用: %v (状态 %d) %s", err, code, raw))
		return
	}
	pass("unlisted 往返", "清单隐藏 / 显式请求可用 / 已恢复")
}

// doWithKey 用指定 Bearer 发请求（调用方密钥验证用）。
func doWithKey(method, path, key string, body any) (int, string, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequest(method, *addr+path, bytes.NewReader(b))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{Timeout: *timeout}).Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), nil
}

// checkOpsEndpoints 验证运维探针与构建信息。
func checkOpsEndpoints() {
	resp, err := (&http.Client{Timeout: *timeout}).Get(*addr + "/healthz")
	if err != nil {
		fail("/healthz", err.Error())
	} else {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || strings.TrimSpace(string(body)) != "ok" {
			fail("/healthz", fmt.Sprintf("状态 %d / body %q", resp.StatusCode, body))
		} else {
			pass("/healthz", "存活探针 200 ok")
		}
	}

	var ready struct {
		Ready    bool     `json:"ready"`
		Accounts int      `json:"accounts"`
		Models   int      `json:"models"`
		Reasons  []string `json:"reasons"`
	}
	code, _, err := doJSON(http.MethodGet, "/readyz", nil, &ready)
	if err != nil {
		fail("/readyz", err.Error())
	} else if code != 200 && code != 503 {
		fail("/readyz", fmt.Sprintf("状态 %d", code))
	} else if ready.Ready != (code == 200) {
		fail("/readyz", fmt.Sprintf("ready=%v 与状态 %d 不符", ready.Ready, code))
	} else if ready.Accounts > 0 && !ready.Ready {
		fail("/readyz", fmt.Sprintf("有 %d 个账号却未就绪: %v", ready.Accounts, ready.Reasons))
	} else {
		pass("/readyz", fmt.Sprintf("ready=%v 账号 %d 模型 %d", ready.Ready, ready.Accounts, ready.Models))
	}

	var ver struct {
		Version  string `json:"version"`
		Go       string `json:"go"`
		Revision string `json:"revision"`
	}
	code, _, err = doJSON(http.MethodGet, "/version", nil, &ver)
	if err != nil || code != 200 || ver.Version == "" || ver.Go == "" {
		fail("/version", fmt.Sprintf("%v (状态 %d)", err, code))
		return
	}
	rev := ver.Revision
	if len(rev) > 12 {
		rev = rev[:12]
	}
	pass("/version", fmt.Sprintf("%s · %s · %s", ver.Version, ver.Go, rev))
}
