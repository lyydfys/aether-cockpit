package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
)

const (
	// defaultMaxTokens 是 max_tokens 的兜底值。Anthropic 的 max_tokens 必填
	// 且计入思考 token；8192 对对话与工具轮次都足够安全（不会撞模型输出上限）。
	defaultMaxTokens = 8192
	// 思考预算下限：Anthropic 要求 budget_tokens >= 1024。
	minThinkingBudget = 1024
)

// defaultUserAgent 是订阅流量的默认 UA。版本必须 >= 上游的客户端版本地板
// （sub2api 内置基线 CLICurrentVersion = 2.1.258）：上游按模型设定最低
// claude-cli 版本（如 sonnet/opus 新模型要求 >= 2.1.251），版本过低会被
// 判非正版客户端并持续 429 且不带 reset 头，且指纹一旦污染无法重置。
// 值可通过 credentials.user_agent 覆盖（覆盖语义不变）。
const defaultUserAgent = "claude-cli/2.1.258 (external, cli)"

// thinkingBudget 把 IR 的 ReasoningEffort 映射为 Anthropic 的思考预算。
// Anthropic 没有 effort 概念，只有 budget_tokens（>=1024 且 <= max_tokens）。
func thinkingBudget(effort string) int {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		return 4096
	case "high":
		return 16384
	default: // medium 与空值
		return 8192
	}
}

// resolveModel 从 IR 请求里取出真实模型名与"是否启用思考"。
//
// 约定：manifest 登记的 "-thinking" 变体的 UpstreamModel 带后缀；
// Anthropic API 的 model 参数不接受该后缀，必须在发请求前剥掉，
// 并翻译成请求体里的 thinking 参数。
func resolveModel(req *ir.UnifiedChatRequest) (model string, thinking bool) {
	raw := strings.TrimSpace(req.UpstreamModel)
	if raw == "" {
		raw = strings.TrimPrefix(strings.TrimSpace(req.Model), channelID+"/")
	}
	thinking = strings.HasSuffix(raw, "-thinking")
	return strings.TrimSuffix(raw, "-thinking"), thinking
}

// ChatStream 把 IR 请求翻译成 Anthropic Messages 请求并解析响应。
//
// 上游是否流式完全跟随 req.Stream：stream=true 走 SSE，
// stream=false 上游返回单个 JSON 对象，本包一次性拆成 IR 事件。
// 网关对非流式请求也会调 ChatStream，再由 Aggregate 收敛成完整响应，
// 因此两条路径都必须实现。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	// 两种凭证形态：
	//   - api_key（sk-ant-api…）：官方按量 API，用 x-api-key，不需要 OAuth 流程；
	//   - access_token / session_key：订阅 OAuth，走原有刷新与换取链路。
	keyMode := strings.TrimSpace(env.GetCredential("api_key"))
	var token string
	if keyMode != "" {
		token = keyMode
	} else {
		// sessionKey 懒换取：credentials 只有 session_key（无 access_token）时，
		// 先用浏览器 Cookie 走三步流自动换 OAuth 凭证（见 sessionkey.go），
		// 再继续正常链路。
		if err := p.ensureOAuthFromSessionKey(ctx, env); err != nil {
			return nil, err
		}
		var err error
		token, err = p.ensureAccessToken(ctx, env)
		if err != nil {
			return nil, err
		}
	}

	model, thinking := resolveModel(req)
	if model == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Claude 订阅缺少模型名").WithProvider(channelID)
	}

	// beta 头先定稿再做 body sanitize：头与体的能力声明必须一致，
	// 不一致会被上游判为非法客户端（400）或非正版（风控）。
	beta := resolveBetaHeader(env)
	ua := resolveUserAgent(env)
	body := buildRequestBody(req, model, thinking)
	if keyMode == "" {
		// beta 头与请求体能力声明必须一致；API Key 不声明 beta，也不裁剪 body。
		_, body = sanitizeBetaHeaders(beta, body)
		// OAuth 订阅流量补 Claude Code 的 billing attribution（cc_version 指纹
		// 与 UA 版本、首条 user 文本绑定）；自定义非 CLI UA 不伪装。
		injectClaudeBillingHeader(body, ua)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Claude 请求序列化失败").WithCause(err)
	}

	resp, err := p.doRequest(ctx, env, token, beta, payload, keyMode != "", ua, req.Stream)
	if err != nil {
		return nil, err
	}
	// 401 可能是 token 刚好过期（比如手动粘贴的长时效 token 没有过期元数据）：
	// 有 refresh_token 时刷新一次再重试，避免让客户端看到一次无谓的失败。
	if keyMode == "" && resp.StatusCode == http.StatusUnauthorized && strings.TrimSpace(env.GetCredential("refresh_token")) != "" {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		if refreshErr := p.refreshAccessToken(ctx, env, env.GetCredential("refresh_token")); refreshErr == nil {
			resp, err = p.doRequest(ctx, env, env.GetCredential("access_token"), beta, payload, false, ua, req.Stream)
			if err != nil {
				return nil, err
			}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, mapUpstreamError(resp.StatusCode, string(raw), resp.Header).WithProvider(channelID)
	}

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		if req.Stream {
			parseSSE(ctx, out, resp.Body)
			return
		}
		parseJSON(ctx, out, resp.Body)
	}()
	return out, nil
}

// baseURL 返回账号级上游地址；凭证显式填 base_url 时优先（自建中转 / 测试）。
func baseURL(env *provider.AccountEnv) string {
	if v := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/"); v != "" {
		return v
	}
	return defaultAPIBaseURL
}

func (p *Provider) doRequest(ctx context.Context, env *provider.AccountEnv, token, beta string, body []byte, keyMode bool, ua string, stream bool) (*http.Response, error) {
	endpoint := baseURL(env) + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Claude 请求失败").WithCause(err)
	}

	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if keyMode {
		// 官方 API Key 默认 x-api-key；Anthropic 兼容厂商（如 Zhipu）
		// 可通过 api_key_auth=bearer 切换为 Authorization: Bearer。
		if strings.EqualFold(strings.TrimSpace(env.GetCredential("api_key_auth")), "bearer") {
			httpReq.Header.Set("Authorization", "Bearer "+token)
		} else {
			httpReq.Header.Set("x-api-key", token)
		}
	} else {
		// 订阅 token 的三个关键头：
		//   - Authorization: Bearer（不是 x-api-key，那是 API key 的用法）
		//   - anthropic-beta: 默认对齐真实 Claude Code CLI 抓包形态（见 defaultBetaHeader），
		//     可用凭证里的 anthropic_beta 覆盖
		//   - anthropic-version: 2023-06-01（官方固定版本头）
		httpReq.Header.Set("Authorization", "Bearer "+token)
		httpReq.Header.Set("anthropic-beta", beta)
		if stream {
			httpReq.Header.Set("Accept", "text/event-stream")
		}
		// 订阅流量官方侧只放开给 Claude Code 客户端：UA + 完整 CLI 头 +
		// 请求体 billing attribution 必须互相一致（见 mimicry.go）。
		httpReq.Header.Set("User-Agent", ua)
		applyClaudeMimicHeaders(httpReq.Header, stream)
	}

	client := env.Client()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 上游连接失败").
			WithCause(err).WithProvider(channelID).WithRetryable(true)
	}
	return resp, nil
}

// --- 请求体构造（IR -> Anthropic Messages） ---

// beta 能力 token：anthropic-beta 头里声明客户端能力的开关，body 里的
// 对应字段只有在头里声明了能力才允许出现，否则上游 400。
const (
	betaInterleavedThinking = "interleaved-thinking-2025-05-14"
	betaContextManagement   = "context-management-2025-06-27"
	betaServerSideFallback  = "server-side-fallback-2026-07-01"
)

// defaultBetaHeader 是订阅流量的默认 anthropic-beta 头，对齐 sub2api 的
// DefaultBetaHeader（真实 Claude Code CLI 抓包形态）：oauth-2025-04-20 是
// 订阅 token 的硬性要求（缺了 401，见 auth.go 的 oauthBetaHeader 注释），
// interleaved-thinking 覆盖本包 thinking 字段的能力声明，
// fine-grained-tool-streaming 覆盖流式 tool_use 参数增量。
// 可用凭证里的 anthropic_beta 整体覆盖（自建中转）。
const defaultBetaHeader = "claude-code-20250219," + oauthBetaHeader + "," +
	betaInterleavedThinking + "," +
	"prompt-caching-scope-2026-01-05,effort-2025-11-24," +
	betaContextManagement + ",thinking-binding-controls-2026-08-01," +
	"mid-conversation-output-config-2026-07-01,extended-cache-ttl-2025-04-11"

// resolveBetaHeader 返回本请求最终使用的 anthropic-beta 头：
// 凭证里的 anthropic_beta（自建中转 / 手动策略）优先，否则用默认值。
func resolveBetaHeader(env *provider.AccountEnv) string {
	if v := strings.TrimSpace(env.GetCredential("anthropic_beta")); v != "" {
		return v
	}
	// spec 热更新：上游调整 beta 能力串时改 JSON 即可，不用重新编译。
	// 仍然会经过 sanitizeBetaHeaders 做头/体对齐。
	if sp := specLoader(channelID); sp != nil {
		for _, k := range []string{"anthropic-beta", "Anthropic-Beta"} {
			if v := strings.TrimSpace(sp.HeadersTemplate[k]); v != "" {
				return v
			}
		}
	}
	return defaultBetaHeader
}

// specLoader 是 spec 读取入口（测试可替换为固定返回值）。
var specLoader = spec.Load

// resolveUserAgent 返回本请求的 UA。
//
// 优先级：账号凭证 user_agent（单账号特例）> spec.client_version（热更新）
// > 内置版本地板。上游按模型设最低 CLI 版本，升版只需要改 spec JSON。
func resolveUserAgent(env *provider.AccountEnv) string {
	if v := strings.TrimSpace(env.GetCredential("user_agent")); v != "" {
		return v
	}
	if sp := specLoader(channelID); sp != nil {
		if v := strings.TrimSpace(sp.ClientVersion); v != "" {
			return "claude-cli/" + v + " (external, cli)"
		}
	}
	return defaultUserAgent
}

// sanitizeBetaHeaders 做 anthropic-beta 头与请求体的双向能力对齐（纯函数，
// 参照 sub2api 的 sanitizeAnthropicBodyForBetaTokens / computeFinalAnthropicBeta）：
//   - 头里未声明某能力、而 body 带了该能力字段：剥掉 body 字段（上游会 400）；
//   - 头里声明了能力、body 没有对应字段：合法，不动。
//
// beta 原样返回（本函数只做单向收敛：能力声明了但没用到没有副作用，
// 反过来"用了才去补头"会掩盖配置错误，交给调用方显式覆盖 anthropic_beta）。
func sanitizeBetaHeaders(beta string, body map[string]any) (string, map[string]any) {
	if body == nil {
		return beta, body
	}
	tokens := betaTokenSet(beta)
	// 字段 -> 守卫它的 beta token（任一命中即保留）。与 buildRequestBody 的
	// 顶层字段一一对应；context_management / fallbacks 本包构造器不产生，
	// 保留规则用于防御手工构造或上游透传的 body。
	rules := []struct {
		field  string
		guards []string
	}{
		{"thinking", []string{betaInterleavedThinking}},
		{"context_management", []string{betaContextManagement}},
		{"fallbacks", []string{betaServerSideFallback}},
	}
	for _, rule := range rules {
		if _, exists := body[rule.field]; !exists {
			continue
		}
		if !containsAnyBetaToken(tokens, rule.guards) {
			delete(body, rule.field)
		}
	}
	return beta, body
}

// betaTokenSet 把逗号分隔的 anthropic-beta 头拆成 token 集合。
func betaTokenSet(beta string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, part := range strings.Split(beta, ",") {
		if t := strings.ToLower(strings.TrimSpace(part)); t != "" {
			out[t] = struct{}{}
		}
	}
	return out
}

// containsAnyBetaToken 判断头 token 集合里是否含有 guards 中任意一个。
func containsAnyBetaToken(tokens map[string]struct{}, guards []string) bool {
	for _, g := range guards {
		if _, ok := tokens[strings.ToLower(g)]; ok {
			return true
		}
	}
	return false
}

// buildRequestBody 构造标准 Anthropic Messages 请求体。
//
// 与 OpenAI 的三点结构差异在这里消化：
//   - system 是顶层字段（IR 的 Instructions 与残留 system 消息合并进去）；
//   - 工具结果是 user 消息里的 tool_result 块，且必须排在消息内容最前；
//   - thinking 块带签名，只能出现在 assistant 消息的第一位。
func buildRequestBody(req *ir.UnifiedChatRequest, model string, thinking bool) map[string]any {
	maxTokens := defaultMaxTokens
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		maxTokens = *req.MaxOutputTokens
	}

	body := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   toAnthropicMessages(req),
		"stream":     req.Stream,
	}
	if system := buildSystem(req); len(system) > 0 {
		body["system"] = system
	}
	if tools := toAnthropicTools(req.Tools); len(tools) > 0 {
		body["tools"] = tools
	}
	if tc := anthropicToolChoice(req); tc != nil {
		body["tool_choice"] = tc
	}
	if len(req.StopSequences) > 0 {
		body["stop_sequences"] = req.StopSequences
	}

	if thinking {
		// thinking 与 temperature/top_p 互斥（开启思考时上游只接受 temperature=1），
		// 显式丢弃，避免 400。
		budget := thinkingBudget(req.ReasoningEffort)
		if budget < minThinkingBudget {
			budget = minThinkingBudget
		}
		// Anthropic 要求 max_tokens > budget_tokens（总输出包含思考部分）。
		if mt, _ := body["max_tokens"].(int); mt <= budget {
			body["max_tokens"] = budget + 2048
		}
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
	} else {
		if req.Temperature != nil {
			body["temperature"] = *req.Temperature
		}
		if req.TopP != nil {
			body["top_p"] = *req.TopP
		}
	}
	return body
}

// buildSystem 把 Instructions 与残留的 system 角色消息合并成 system 块数组。
func buildSystem(req *ir.UnifiedChatRequest) []any {
	var parts []string
	if s := strings.TrimSpace(req.Instructions); s != "" {
		parts = append(parts, s)
	}
	for _, item := range req.Items {
		if item.Type == ir.ItemTypeMessage && normalizeRole(item.Role) == "system" {
			if s := strings.TrimSpace(item.Text); s != "" {
				parts = append(parts, s)
			}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	block := map[string]any{"type": "text", "text": strings.Join(parts, "\n\n")}
	return []any{applyCacheControl(block, req.SystemCacheControl)}
}

// applyCacheControl 把 IR 里保留的 Anthropic 缓存断点原样写回内容块。
func applyCacheControl(block map[string]any, cc json.RawMessage) map[string]any {
	if len(cc) == 0 {
		return block
	}
	var v any
	if json.Unmarshal(cc, &v) == nil {
		block["cache_control"] = v
	}
	return block
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system", "developer":
		return "system"
	case "assistant", "model":
		return "assistant"
	case "tool", "function":
		return "tool"
	default:
		return "user"
	}
}

// toAnthropicMessages 把 IR Item 列表翻译成 Anthropic messages 数组。
//
// Anthropic 要求 user / assistant 严格交替，因此相邻同角色的块必须合并进
// 同一条消息；思考块（IR 的 reasoning item）附着到同一条 assistant 消息的最前。
func toAnthropicMessages(req *ir.UnifiedChatRequest) []any {
	var (
		out         []any
		role        string
		blocks      []any // 普通内容块（text / image / thinking / tool_use）
		toolResults []any // tool_result 块，flush 时排在消息最前（上游硬性要求）
	)
	flush := func() {
		if len(blocks) == 0 && len(toolResults) == 0 {
			return
		}
		content := make([]any, 0, len(toolResults)+len(blocks))
		content = append(content, toolResults...)
		content = append(content, blocks...)
		out = append(out, map[string]any{"role": role, "content": content})
		blocks, toolResults = nil, nil
	}
	switchTo := func(next string) {
		if role == next {
			return
		}
		flush()
		role = next
	}

	toolSeq := 0
	for _, item := range req.Items {
		switch item.Type {
		case ir.ItemTypeMessage:
			switch normalizeRole(item.Role) {
			case "system":
				// system 消息由 buildSystem 收敛，这里跳过。
				continue
			case "assistant":
				if text := item.Text; strings.TrimSpace(text) != "" {
					switchTo("assistant")
					blocks = append(blocks, applyCacheControl(map[string]any{"type": "text", "text": text}, item.CacheControl))
				}
			default: // user / tool 角色都按 user 侧处理
				if strings.TrimSpace(item.Text) != "" {
					switchTo("user")
					blocks = append(blocks, applyCacheControl(map[string]any{"type": "text", "text": item.Text}, item.CacheControl))
				}
				for _, att := range item.Attachments {
					switchTo("user")
					if block := imageBlock(att); block != nil {
						blocks = append(blocks, applyCacheControl(block, item.CacheControl))
					}
				}
			}

		case ir.ItemTypeReasoning:
			// 加密思考块（redacted_thinking）没有明文，只能原样回传 data。
			if item.EncryptedContent != "" && item.Text == "" && item.ThoughtSignature == "" {
				switchTo("assistant")
				blocks = append(blocks, applyCacheControl(map[string]any{
					"type": "redacted_thinking", "data": item.EncryptedContent,
				}, item.CacheControl))
				continue
			}
			// 思考块只能出现在 assistant 消息里且必须排在其它块之前；
			// IR 的顺序（inbound 已保证 thinking -> text/tool_use）天然满足。
			if item.Text == "" && item.ThoughtSignature == "" {
				continue
			}
			switchTo("assistant")
			block := map[string]any{"type": "thinking", "thinking": item.Text}
			if item.ThoughtSignature != "" {
				// 签名是上游签发的"思考证明"，续轮时必须原样带回，否则 400。
				block["signature"] = item.ThoughtSignature
			}
			blocks = append(blocks, applyCacheControl(block, item.CacheControl))

		case ir.ItemTypeFunctionCall:
			switchTo("assistant")
			toolSeq++
			callID := item.CallID
			if callID == "" {
				// 上游要求 tool_use 必须有 id，且下一轮 tool_result 要引用它。
				callID = fmt.Sprintf("toolu_claude_sub_%d", toolSeq)
			}
			blocks = append(blocks, applyCacheControl(map[string]any{
				"type":  "tool_use",
				"id":    callID,
				"name":  item.Name,
				"input": parseJSONOrEmpty(item.Arguments),
			}, item.CacheControl))

		case ir.ItemTypeFunctionCallOutput:
			switchTo("user")
			result := map[string]any{
				"type":        "tool_result",
				"tool_use_id": item.CallID,
			}
			if item.Output != "" {
				result["content"] = []any{map[string]any{"type": "text", "text": item.Output}}
			}
			toolResults = append(toolResults, applyCacheControl(result, item.CacheControl))
		}
	}
	flush()
	return out
}

// imageBlock 把 IR 附件翻译成 Anthropic 图片块；不支持的附件退化为文字提示。
func imageBlock(att ir.MediaAttachment) map[string]any {
	mime := att.MIME
	if mime == "" {
		mime = "image/png"
	}
	if att.DataB64 != "" {
		return map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": mime,
				"data":       att.DataB64,
			},
		}
	}
	if att.URL != "" {
		return map[string]any{
			"type": "image",
			"source": map[string]any{
				"type": "url",
				"url":  att.URL,
			},
		}
	}
	return map[string]any{"type": "text", "text": "[附件] " + att.FileName}
}

// toAnthropicTools 把 IR 工具定义翻译成 Anthropic 的 input_schema 形态。
func toAnthropicTools(tools []ir.UnifiedToolSpec) []any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		tool := map[string]any{"name": t.Name}
		if t.Description != "" {
			tool["description"] = t.Description
		}
		var schema any
		if len(t.Parameters) > 0 {
			if err := json.Unmarshal(t.Parameters, &schema); err != nil {
				schema = nil
			}
		}
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tool["input_schema"] = schema
		applyCacheControl(tool, t.CacheControl)
		out = append(out, tool)
	}
	return out
}

// anthropicToolChoice 把 IR 的工具选择策略展开成 Anthropic 形态。
// "required" 对应 Anthropic 的 any；function 对应 tool+name。
func anthropicToolChoice(req *ir.UnifiedChatRequest) map[string]any {
	var out map[string]any
	if tc := req.ToolChoice; tc != nil {
		switch tc.Mode {
		case "auto":
			out = map[string]any{"type": "auto"}
		case "none":
			out = map[string]any{"type": "none"}
		case "required":
			out = map[string]any{"type": "any"}
		case "function":
			out = map[string]any{"type": "tool", "name": tc.Name}
		}
	}
	if req.ParallelToolCalls != nil && !*req.ParallelToolCalls {
		if out == nil {
			out = map[string]any{"type": "auto"}
		}
		out["disable_parallel_tool_use"] = true
	}
	return out
}

// parseJSONOrEmpty 把工具参数字符串解析成 JSON 对象；解析失败退化为
// {"_raw": 原文}（对齐 antigravity 的容错策略，宁可透传也不静默丢参）。
func parseJSONOrEmpty(args string) any {
	if strings.TrimSpace(args) == "" {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal([]byte(args), &v); err != nil {
		return map[string]any{"_raw": args}
	}
	return v
}

// --- 响应解析（SSE / 单个 JSON -> IR 事件） ---

// sseState 跨帧维护流式状态：usage 分散在 message_start 与 message_delta，
// 思考签名出现在思考块所有增量之后、工具调用开始之前。
type sseState struct {
	inputTokens   int
	outputTokens  int
	cachedTokens  int
	cacheCreation int
	sawTool       bool
	finish        string
	done          bool

	// blockType / blockTool 按 content_block 的 index 记录类型与工具 id，
	// input_json_delta 只带 index 不带 id，需要查表。
	blockType map[string]string
	blockTool map[string]string
	// pendingSignature 缓存 thinking 块的签名，附着到紧随其后的
	// EventToolCallStart 上（聚合器按 ToolCallID 键控保存签名）。
	pendingSignature string
}

func (s *sseState) typeOf(index any) string {
	key, ok := index.(string)
	if !ok {
		key = fmt.Sprintf("%v", index)
	}
	return s.blockType[key]
}

func (s *sseState) setBlock(index any, typ, toolID string) {
	key := fmt.Sprintf("%v", index)
	if s.blockType == nil {
		s.blockType = map[string]string{}
		s.blockTool = map[string]string{}
	}
	s.blockType[key] = typ
	if toolID != "" {
		s.blockTool[key] = toolID
	}
}

func (s *sseState) toolOf(index any) string {
	key := fmt.Sprintf("%v", index)
	return s.blockTool[key]
}

func (s *sseState) dropBlock(index any) {
	key := fmt.Sprintf("%v", index)
	delete(s.blockType, key)
	delete(s.blockTool, key)
}

// parseSSE 解析 Anthropic 的 SSE 流并产出 IR 事件。
//
// 事件映射关系：
//
//	message_start                  -> EventStart（携带 ResponseID）
//	content_block_start(tool_use)  -> EventToolCallStart
//	delta: text_delta              -> EventTextDelta
//	delta: thinking_delta          -> EventReasoningDelta
//	delta: signature_delta         -> 缓存，附着到下一个工具调用 Start
//	delta: input_json_delta        -> EventToolCallDelta
//	message_delta(stop_reason)     -> 结束原因
//	message_delta(usage)           -> EventUsage（input 取自 message_start）
//	message_stop                   -> EventDone
func parseSSE(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	st := &sseState{}
	emit := func(ev ir.UnifiedStreamEvent) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	// 先发一个 Start：dispatch 靠"首事件非错误"确认渠道可用，
	// 不能等上游首帧（可能慢好几秒）。
	if !emit(ir.UnifiedStreamEvent{Type: ir.EventStart}) {
		return
	}

	dec := wirecodec.NewSSEDecoder(r)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		frame, err := dec.Next()
		if err != nil {
			break
		}
		data := strings.TrimSpace(string(frame.Data))
		if data == "" {
			continue
		}

		var root map[string]any
		if err := json.Unmarshal([]byte(data), &root); err != nil {
			continue
		}
		typ, _ := root["type"].(string)
		if typ == "" {
			typ = frame.Event // SSE event: 字段兜底
		}

		switch typ {
		case "message_start":
			msg, _ := root["message"].(map[string]any)
			id, _ := msg["id"].(string)
			if usage, ok := msg["usage"].(map[string]any); ok {
				st.inputTokens = lookupInt(usage, "input_tokens")
				st.cachedTokens = lookupInt(usage, "cache_read_input_tokens")
				st.cacheCreation = lookupInt(usage, "cache_creation_input_tokens")
			}
			// ResponseID 供状态缝合层使用；Start 事件被三种出站转导器忽略，
			// 这里再发一次只带 ID 的 Start 是安全的。
			if !emit(ir.UnifiedStreamEvent{Type: ir.EventStart, ResponseID: id}) {
				return
			}

		case "content_block_start":
			block, _ := root["content_block"].(map[string]any)
			blockType, _ := block["type"].(string)
			if blockType == "tool_use" {
				st.sawTool = true
				id, _ := block["id"].(string)
				name, _ := block["name"].(string)
				// 记录 index -> 工具 id：后续 input_json_delta 只带 index，
				// 需要靠它找回 ToolCallID。
				st.setBlock(root["index"], blockType, id)
				ev := ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: id, ToolName: name}
				if st.pendingSignature != "" {
					// 思考签名随紧随其后的工具调用交给聚合器落盘，
					// 续轮时才能还原"先思考后调用"的完整 assistant 消息。
					ev.ThoughtSignature = st.pendingSignature
					st.pendingSignature = ""
				}
				if !emit(ev) {
					return
				}
				continue
			}
			st.setBlock(root["index"], blockType, "")

		case "content_block_delta":
			delta, _ := root["delta"].(map[string]any)
			deltaType, _ := delta["type"].(string)
			switch deltaType {
			case "text_delta":
				text, _ := delta["text"].(string)
				if text != "" && !emit(ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: text}) {
					return
				}
			case "thinking_delta":
				text, _ := delta["thinking"].(string)
				if text != "" && !emit(ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: text}) {
					return
				}
			case "signature_delta":
				// 思考块结束时上游补发签名（在所有 thinking_delta 之后）。
				sig, _ := delta["signature"].(string)
				st.pendingSignature = sig
			case "input_json_delta":
				args, _ := delta["partial_json"].(string)
				if args == "" {
					continue
				}
				toolID := st.toolOf(root["index"])
				if toolID == "" {
					// 上游没发 Start 就直接发增量（异常流）：兜底补一个空 Start，
					// 聚合器对"无 Start 的 Delta"也能兜住，但 ID 会缺失。
					if !emit(ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: "", ToolName: ""}) {
						return
					}
				}
				if !emit(ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ToolCallID: toolID, ArgsDelta: args}) {
					return
				}
			}

		case "content_block_stop":
			st.dropBlock(root["index"])

		case "message_delta":
			if delta, ok := root["delta"].(map[string]any); ok {
				if reason, _ := delta["stop_reason"].(string); reason != "" {
					st.finish = mapFinishReason(reason)
				}
			}
			if usage, ok := root["usage"].(map[string]any); ok {
				if n := lookupInt(usage, "output_tokens"); n > 0 {
					st.outputTokens = n // 上游报的是累计值
				}
			}
			if !emit(ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: st.usage()}) {
				return
			}

		case "message_stop":
			st.done = true
			finish := st.finish
			if finish == "" {
				finish = "stop"
			}
			emit(ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
			return

		case "error":
			errObj, _ := root["error"].(map[string]any)
			emit(ir.UnifiedStreamEvent{
				Type: ir.EventError,
				Err:  mapStreamError(errObj).WithProvider(channelID),
			})
			return

		case "ping":
			// 心跳，忽略。
		}
	}

	// 流异常中断（EOF / 解码错误）但没收到 message_stop：补一个 Done，
	// 否则下游聚合会挂到 channel 关闭才结束，finish_reason 缺失。
	if !st.done {
		finish := st.finish
		if finish == "" {
			if st.sawTool {
				finish = "tool_calls"
			} else {
				finish = "stop"
			}
		}
		emit(ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
	}
}

// usage 汇总当前 token 用量。Anthropic 不报 total，自行相加
// （cache_read 也占上下文窗口，计入总量）。
func (s *sseState) usage() *ir.Usage {
	return &ir.Usage{
		InputTokens:         s.inputTokens,
		OutputTokens:        s.outputTokens,
		CachedTokens:        s.cachedTokens,
		CacheCreationTokens: s.cacheCreation,
		TotalTokens:         s.inputTokens + s.outputTokens + s.cachedTokens + s.cacheCreation,
	}
}

// anthropicResponse 是非流式响应的线缆结构。
type anthropicResponse struct {
	ID      string `json:"id"`
	Content []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text"`
		Thinking  string          `json:"thinking"`
		Signature string          `json:"signature"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Input     json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// parseJSON 处理 stream=false 的完整 JSON 响应，按块顺序一次性产出 IR 事件。
func parseJSON(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	emit := func(ev ir.UnifiedStreamEvent) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	raw, err := io.ReadAll(io.LimitReader(r, 32<<10))
	if err != nil {
		emit(ir.UnifiedStreamEvent{
			Type: ir.EventError,
			Err:  ir.NewAPIError(ir.ErrCodeUpstreamFailed, "读取 Claude 响应失败").WithCause(err).WithProvider(channelID),
		})
		return
	}

	var resp anthropicResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		emit(ir.UnifiedStreamEvent{
			Type: ir.EventError,
			Err:  ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Claude 响应无法解析").WithCause(err).WithProvider(channelID),
		})
		return
	}
	if resp.Error != nil {
		emit(ir.UnifiedStreamEvent{
			Type: ir.EventError,
			Err: mapStreamError(map[string]any{
				"type":    resp.Error.Type,
				"message": resp.Error.Message,
			}).WithProvider(channelID),
		})
		return
	}

	emit(ir.UnifiedStreamEvent{Type: ir.EventStart, ResponseID: resp.ID})

	finish := ""
	sawTool := false
	var pendingSig string
	for _, block := range resp.Content {
		switch block.Type {
		case "thinking":
			if block.Thinking != "" {
				if !emit(ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: block.Thinking}) {
					return
				}
			}
			pendingSig = block.Signature
		case "text":
			if block.Text != "" {
				if !emit(ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: block.Text}) {
					return
				}
			}
		case "tool_use":
			sawTool = true
			ev := ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: block.ID, ToolName: block.Name}
			if pendingSig != "" {
				ev.ThoughtSignature = pendingSig
				pendingSig = ""
			}
			if !emit(ev) {
				return
			}
			args := "{}"
			if len(block.Input) > 0 && string(block.Input) != "null" {
				args = string(block.Input)
			}
			if !emit(ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ToolCallID: block.ID, ArgsDelta: args}) {
				return
			}
		}
	}

	if reason := mapFinishReason(resp.StopReason); reason != "" {
		finish = reason
	}
	if finish == "" {
		if sawTool {
			finish = "tool_calls"
		} else {
			finish = "stop"
		}
	}

	usage := &ir.Usage{
		InputTokens:         resp.Usage.InputTokens,
		OutputTokens:        resp.Usage.OutputTokens,
		CachedTokens:        resp.Usage.CacheReadInputTokens,
		CacheCreationTokens: resp.Usage.CacheCreationInputTokens,
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CachedTokens + usage.CacheCreationTokens
	emit(ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: usage})
	emit(ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
}

// --- 错误映射 ---

// mapUpstreamError 把 HTTP 层错误翻译成 IR 统一错误码。
//
// Anthropic 的错误体固定为 {"type":"error","error":{"type":..., "message":...}}。
// 映射规则对齐 workbuddy：401/403 鉴权、429 限流（可换号重试）、
// 4xx 请求本身有问题、5xx 上游故障（可重试）。529 是 Anthropic 特有的
// overloaded（过载），语义等同 5xx。
func mapUpstreamError(status int, body string, hdr http.Header) *ir.APIError {
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(body), &payload)
	detail := strings.TrimSpace(payload.Error.Message)
	if detail == "" {
		detail = truncate(strings.TrimSpace(body), 300)
	}

	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		// 401 常见于：token 过期、缺 oauth beta 头、token 被吊销。
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Claude 订阅鉴权失败（%d）：%s", status, detail)
	case status == http.StatusTooManyRequests:
		// 订阅共享最容易撞的就是限流：明确可重试，让路由层换账号或稍后再试。
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "Claude 订阅限流：%s", detail).
			WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, detail))
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity ||
		status == http.StatusNotFound || status == http.StatusRequestEntityTooLarge:
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "Claude 拒绝请求：%s", detail)
	case status == 529 || status >= 500:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Claude 上游 %d：%s", status, detail).
			WithRetryable(true).WithStatus(http.StatusBadGateway)
	default:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Claude 上游返回 %d：%s", status, detail)
	}
}

// mapStreamError 把流内 error 事件的 type 翻译成 IR 错误码。
func mapStreamError(errObj map[string]any) *ir.APIError {
	typ, _ := errObj["type"].(string)
	msg, _ := errObj["message"].(string)
	if msg == "" {
		if b, err := json.Marshal(errObj); err == nil {
			msg = string(b)
		}
	}
	code := ir.ErrCodeUpstreamFailed
	retryable := false
	switch typ {
	case "authentication_error", "permission_error":
		code = ir.ErrCodeUpstreamAuth
	case "rate_limit_error":
		code = ir.ErrCodeUpstreamRateLimit
		retryable = true
	case "invalid_request_error":
		code = ir.ErrCodeBadRequest
	case "overloaded_error":
		retryable = true // 过载是瞬时的，可重试
	}
	ae := ir.NewAPIErrorf(code, "Claude 流内错误（%s）：%s", typ, msg)
	return ae.WithRetryable(retryable)
}

// mapFinishReason 把 Anthropic 的 stop_reason 翻译成 IR 的结束原因。
func mapFinishReason(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens", "model_context_window_exceeded":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	case "pause_turn":
		// 上游要求下一轮续传：对下游表现为正常结束，由调用方带历史重发。
		return "stop"
	default:
		return "stop"
	}
}

func lookupInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}
