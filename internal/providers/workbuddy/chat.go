package workbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
)

// ChatStream 调用 WorkBuddy 的 OpenAI 兼容流式端点。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	// 命中过风控的渠道在冷静期内直接拒绝，避免"连续撞墙"把可疑升级成确认滥用。
	if until := pausedUntil(p.profile.id); !until.IsZero() {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamRateLimit,
			"该渠道刚触发上游风控（11140/403），已暂停 "+time.Until(until).Round(time.Second).String()+"；请勿频繁重试，稍后再试或换账号").
			WithProvider(p.profile.id)
	}
	token := firstNonEmpty(
		strings.TrimSpace(env.GetCredential("token")),
		strings.TrimSpace(env.GetCredential("access_token")),
	)
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "WorkBuddy 账号缺少 token").
			WithProvider(p.profile.id)
	}
	userID := strings.TrimSpace(env.GetCredential("user_id"))
	if userID == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "WorkBuddy 账号缺少 user_id（请求头 X-User-Id 必需）").
			WithProvider(p.profile.id)
	}

	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = stripProviderPrefix(req.Model, p.profile.id)
	}
	if model == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "WorkBuddy 缺少模型名").
			WithProvider(p.profile.id)
	}

	payload := buildPayload(req, model, p.profile.intl)
	// 提示词指纹清洗：仅在账号显式开启时生效（见 scrub.go）。
	// 把 Claude Code / OpenCode 的开场白指纹换成中性描述——对 WorkBuddy 上游来说，
	// 系统提示里写着别家客户端名字，等于自报"我不是官方客户端"。
	if scrubEnabled(env.GetCredential) {
		_ = scrubClaudeFingerprint(payload)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "WorkBuddy 请求序列化失败").WithCause(err)
	}

	client := env.Client()
	// 本次请求使用哪个身份档位只解析一次：UA/头/间隔必须来自同一个 profile。
	prof := currentIdentity()
	var resp *http.Response
	// 最多两次：第一次用会话哈希出的 id，被 11128 拦下时换一组全新 id 再试。
	// 上游的 displayMsg 是"请求被拦截，请重新发送"，但它按会话/请求 id 记住
	// 被拦的那一次，原样重发还是同一条 id，只会再被拦——所以必须换 id。
	for attempt := 0; attempt < 2; attempt++ {
		// 同账号请求节流：真实客户端按交互节奏发请求，机器式连发是风控特征之一。
		// 间隔来自档位（cli/desktop=350ms，quiet=900ms）。
		if wait := paceWait(env.AccountID, prof.PaceGap); wait > 0 {
			select {
			case <-ctx.Done():
				return nil, ir.NewAPIError(ir.ErrCodeInternal, "请求已取消").WithCause(ctx.Err())
			case <-time.After(wait):
			}
		}
		httpReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost,
			p.baseURL(env)+"/chat/completions", bytes.NewReader(body))
		if reqErr != nil {
			return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 WorkBuddy 请求失败").WithCause(reqErr)
		}
		p.applyHeaders(httpReq, env, prof, token, userID, req, attempt)

		resp, err = client.Do(httpReq)
		if err != nil {
			return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "WorkBuddy 上游连接失败").
				WithCause(err).WithProvider(p.profile.id).WithRetryable(true)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			break
		}
		status, hdr := resp.StatusCode, resp.Header
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		resp = nil
		// 错误码 → 账号级冷却（表驱动，见 cooldown.go）。先冷却账号，再由
		// 路由换号：既不继续撞同一个号，也不把整条渠道停掉。
		cool, reason := wbCooldown(status, string(raw))
		if cool > 0 {
			env.MarkCoolingDown(cool, reason)
		}
		if attempt == 0 && channelBlocked(string(raw)) {
			continue
		}
		if riskBlocked(status, string(raw)) {
			// 风控信号：按下暂停键，让整个渠道冷静一段时间；继续重试只会让
			// "可疑"升级成"确认滥用"，还可能连坐同区域其它账号。
			pauseProvider(p.profile.id, riskPauseDefault)
			return nil, ir.NewAPIError(ir.ErrCodeUpstreamRateLimit,
				"上游判定为异常行为（风控），已暂停该渠道 "+riskPauseDefault.Round(time.Minute).String()+"；请优先使用官方客户端，并避免多账号共用同一出口").
				WithProvider(p.profile.id)
		}
		return nil, mapUpstreamError(status, string(raw), hdr).WithProvider(p.profile.id)
	}
	if resp == nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "WorkBuddy 拒绝本次调用（通道校验未通过）").
			WithProvider(p.profile.id)
	}

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		parseSSE(ctx, out, resp.Body)
	}()
	return out, nil
}

func (p *Provider) baseURL(env *provider.AccountEnv) string {
	if v := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/"); v != "" {
		return v
	}
	return p.profile.baseURL
}

func (p *Provider) domain(env *provider.AccountEnv) string {
	if v := strings.TrimSpace(env.GetCredential("domain")); v != "" {
		return v
	}
	return p.profile.domain
}

// applyHeaders 写入 WorkBuddy 对话请求头。
//
// 组成顺序对齐参考实现 Upstream.chatHeaders：
// 基础头（UA/插件头/设备令牌/企业头）→ 会话头 → Stainless（仅 CLI 档）
// → 通道校验头（trace/B3/baggage + X-Conversation-Request-ID）。
//
// 参考实现不发送 X-Agent-Type / X-Device-Id（REA 检索确认），这里也不发。
func (p *Provider) applyHeaders(req *http.Request, env *provider.AccountEnv, prof IdentityProfile, token, userID string, chat *ir.UnifiedChatRequest, attempt int) {
	h := req.Header
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("Authorization", "Bearer "+token)
	h.Set("X-User-Id", userID)
	h.Set("X-Domain", p.domain(env))
	setPluginHeaders(h, prof)
	for k, v := range machineHeadersFor(prof, env.GetCredential) {
		h.Set(k, v)
	}
	if v := strings.TrimSpace(env.GetCredential("device_token")); v != "" {
		h.Set("X-Device-Token", v)
	}
	enterprise := strings.TrimSpace(env.GetCredential("enterprise_id"))
	if enterprise != "" {
		h.Set("X-Enterprise-Id", enterprise)
		// 参考实现把 X-Tenant-Id 也填成 enterprise；保留 tenant_id 覆盖能力
		// 以兼容企业版账号既有配置。
		h.Set("X-Tenant-Id", enterprise)
	}
	if v := strings.TrimSpace(env.GetCredential("tenant_id")); v != "" {
		h.Set("X-Tenant-Id", v)
	}

	conv := conversationIDFor(env.AccountID)
	msg := uuidV7()
	reqID := uuidV7()
	setConversationHeaders(h, conv, msg)
	applyStainlessHeaders(h, prof)
	applyChannelVerifyHeaders(h, conv, reqID)
}

// buildPayload 把 IR 转成 OpenAI Chat Completions 请求体。
//
// WorkBuddy Intl 的通道校验要求首条是 system prompt，缺失会被 11128 拦截；
// 官方客户端总会带系统提示，这里在缺省时补一条最小 system。
func buildPayload(req *ir.UnifiedChatRequest, model string, intl bool) map[string]any {
	messages := toMessages(req)
	if intl && (len(messages) == 0 || messages[0]["role"] != "system") {
		messages = append([]map[string]any{{"role": "system", "content": "You are a helpful assistant."}}, messages...)
	}
	payload := map[string]any{
		"model":    model,
		"messages": messages,
		// WorkBuddy 只支持流式；网关始终走 ChatStream。
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}

	if len(req.Tools) > 0 {
		payload["tools"] = toTools(req.Tools)
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		payload["top_p"] = *req.TopP
	}
	// 重复惩罚照实转发：agent 客户端普遍会带，丢掉以后长输出容易复读。
	if req.FrequencyPenalty != nil {
		payload["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.PresencePenalty != nil {
		payload["presence_penalty"] = *req.PresencePenalty
	}
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		payload["max_tokens"] = *req.MaxOutputTokens
	}
	if len(req.StopSequences) > 0 {
		payload["stop"] = req.StopSequences
	}
	if req.ReasoningEffort != "" {
		if !req.EffortAuto {
			payload["reasoning_effort"] = req.ReasoningEffort
		}
	}
	return payload
}

// toMessages 把 IR Item 列表转成 OpenAI messages。
//
// 思考块累积后挂到下一条 assistant 消息的 reasoning_content 上；
// assistant 带 tool_calls 时 content 必须是 null，否则部分上游会 400。
func toMessages(req *ir.UnifiedChatRequest) []map[string]any {
	msgs := make([]map[string]any, 0, len(req.Items)+1)
	if s := strings.TrimSpace(req.Instructions); s != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": s})
	}

	var pendingCalls []map[string]any
	var pendingReasoning string

	flushCalls := func() {
		if len(pendingCalls) == 0 && pendingReasoning == "" {
			return
		}
		msg := map[string]any{"role": "assistant"}
		if len(pendingCalls) > 0 {
			msg["content"] = nil
			msg["tool_calls"] = pendingCalls
		} else {
			msg["content"] = ""
		}
		if pendingReasoning != "" {
			msg["reasoning_content"] = pendingReasoning
		}
		msgs = append(msgs, msg)
		pendingCalls = nil
		pendingReasoning = ""
	}

	for _, item := range req.Items {
		switch item.Type {
		case ir.ItemTypeMessage:
			role := item.Role
			if role == "" {
				role = "user"
			}
			// 思考块优先附着到紧接着的 assistant 文本消息上。
			if role == "assistant" && len(pendingCalls) == 0 {
				msg := map[string]any{"role": role, "content": item.Text}
				if pendingReasoning != "" {
					msg["reasoning_content"] = pendingReasoning
					pendingReasoning = ""
				}
				msgs = append(msgs, msg)
				continue
			}
			flushCalls()
			msgs = append(msgs, map[string]any{"role": role, "content": item.Text})

		case ir.ItemTypeReasoning:
			if item.Text != "" {
				pendingReasoning += item.Text
			}

		case ir.ItemTypeFunctionCall:
			pendingCalls = append(pendingCalls, map[string]any{
				"id":   item.CallID,
				"type": "function",
				"function": map[string]any{
					"name":      item.Name,
					"arguments": defaultArguments(item.Arguments),
				},
			})

		case ir.ItemTypeFunctionCallOutput:
			flushCalls()
			msgs = append(msgs, map[string]any{
				"role":         "tool",
				"tool_call_id": item.CallID,
				"content":      item.Output,
			})
		}
	}
	flushCalls()
	return msgs
}

func toTools(tools []ir.UnifiedToolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		fn := map[string]any{"name": tool.Name}
		if tool.Description != "" {
			fn["description"] = tool.Description
		}
		if len(tool.Parameters) > 0 {
			var schema any
			if err := json.Unmarshal(tool.Parameters, &schema); err == nil {
				fn["parameters"] = schema
			}
		}
		if _, ok := fn["parameters"]; !ok {
			fn["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

func defaultArguments(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

func stripProviderPrefix(model, providerID string) string {
	prefix := providerID + "/"
	if strings.HasPrefix(model, prefix) {
		return strings.TrimPrefix(model, prefix)
	}
	return model
}

func firstUserText(req *ir.UnifiedChatRequest) string {
	for _, item := range req.Items {
		if item.IsUserTurn() {
			return item.Text
		}
	}
	return ""
}

// openAIChunk 是 WorkBuddy 流式响应的单个 chunk。
type openAIChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// parseSSE 解析 OpenAI SSE 流，产出 IR 事件。
func parseSSE(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	dec := wirecodec.NewSSEDecoder(r)

	toolIndexToID := make(map[int]string, 4)
	startedTool := make(map[int]bool, 4)
	finishReason := ""

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
		if data == "[DONE]" {
			break
		}

		var chunk openAIChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if chunk.Error != nil {
			emit(ctx, out, ir.UnifiedStreamEvent{
				Type: ir.EventError,
				Err:  ir.NewAPIError(ir.ErrCodeUpstreamFailed, "WorkBuddy 流内错误: "+chunk.Error.Message),
			})
			return
		}

		if chunk.Usage != nil {
			usage := &ir.Usage{OutputTokens: chunk.Usage.CompletionTokens}
			if chunk.Usage.PromptTokensDetails != nil {
				usage.CachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
			}
			// WorkBuddy 的 prompt_tokens 含 cached_tokens，先拆开再落库；
			// 否则命中量算两遍，总量翻倍、命中率永远不到 50%。
			usage.InputTokens = ir.SplitPromptCache(chunk.Usage.PromptTokens, usage.CachedTokens)
			if chunk.Usage.CompletionTokensDetails != nil {
				usage.ReasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
			}
			usage.NormalizeTotal()
			emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: usage})
		}

		for _, choice := range chunk.Choices {
			if text := firstNonEmpty(choice.Delta.ReasoningContent, choice.Delta.Reasoning); text != "" {
				emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: text})
			}
			if choice.Delta.Content != "" {
				emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: choice.Delta.Content})
			}

			for _, call := range choice.Delta.ToolCalls {
				id := call.ID
				if id == "" {
					id = toolIndexToID[call.Index]
				} else {
					toolIndexToID[call.Index] = id
				}

				if !startedTool[call.Index] && call.Function.Name != "" {
					startedTool[call.Index] = true
					emit(ctx, out, ir.UnifiedStreamEvent{
						Type:       ir.EventToolCallStart,
						ToolCallID: id,
						ToolName:   call.Function.Name,
					})
				}
				if call.Function.Arguments != "" {
					if !startedTool[call.Index] {
						startedTool[call.Index] = true
						emit(ctx, out, ir.UnifiedStreamEvent{
							Type:       ir.EventToolCallStart,
							ToolCallID: id,
							ToolName:   "",
						})
					}
					emit(ctx, out, ir.UnifiedStreamEvent{
						Type:       ir.EventToolCallDelta,
						ToolCallID: id,
						ArgsDelta:  call.Function.Arguments,
					})
				}
			}

			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
	}

	if finishReason == "" {
		if len(startedTool) > 0 {
			finishReason = "tool_calls"
		} else {
			finishReason = "stop"
		}
	}
	emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finishReason})
}

func emit(ctx context.Context, out chan<- ir.UnifiedStreamEvent, ev ir.UnifiedStreamEvent) {
	select {
	case out <- ev:
	case <-ctx.Done():
	}
}

// --- 错误映射 ---

// workbuddyBadRequestCodes 是 WorkBuddy 返回"请求本身不合法"的业务码。
// 这些错误换账号重试没有意义，直接透传为 bad_request。
//
// 11140/11142 是风控码，不在这里：它们走 cooldown.go 的账号冷却 + 渠道暂停。
var workbuddyBadRequestCodes = map[string]bool{
	"10085": true,
	"11128": true,
	"11133": true,
	"11153": true,
}

// channelBlocked 判断上游是不是用 11128（通道校验）拦了这次调用。
//
// 这类拒绝不计费、也不代表账号有问题：上游按会话/请求 id 记住被拦的那一次，
// 换一组新 id 重放通常就能过（它自己的 displayMsg 也只说"请重新发送"）。
func channelBlocked(raw string) bool {
	return strings.Contains(raw, `"code":11128`) || strings.Contains(raw, `"code": 11128`)
}

func mapUpstreamError(status int, body string, hdr http.Header) *ir.APIError {
	code, message := parseUpstreamError(body)
	detail := message
	if detail == "" {
		detail = truncate(strings.TrimSpace(body), 300)
	}

	switch {
	case code == "11140" || code == "11142":
		// 风控码：账号已按 cooldown.go 冷却；标成可重试，让路由换号而不是
		// 把整条请求判死。
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "WorkBuddy 风控拦截（%s）：%s", code, detail).
			WithRetryable(true).WithRetryAfter(30 * time.Minute)
	case code == "14015":
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "WorkBuddy 登录态过期（14015）：%s", detail).
			WithRetryAfter(wbCooldownRetry(status, body))
	case isWBMeterLimitCode(code):
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "WorkBuddy 额度/用量上限（%s）：%s", code, detail).
			WithRetryable(true).WithRetryAfter(1440 * time.Minute)
	case isWBEmptyCode(code):
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "WorkBuddy 上游未返回有效内容（%s）：%s", code, detail).
			WithRetryable(true).WithRetryAfter(60 * time.Minute)
	case status == http.StatusUnauthorized:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "WorkBuddy 鉴权失败（%d）：%s", status, detail).
			WithRetryAfter(wbCooldownRetry(status, body))
	case status == http.StatusForbidden:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "WorkBuddy 鉴权失败（%d）：%s", status, detail)
	case status == http.StatusTooManyRequests:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "WorkBuddy 限流：%s", detail).
			WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, detail))
	case code == "11158" || strings.Contains(strings.ToLower(detail), "is not allowed for user type"):
		// 模型不对当前账号类型开放（如 free 账号选了 Pro 模型）：换账号/
		// 换渠道试，不能当成请求格式错误直接终止整条路由。
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "WorkBuddy 账号类型不支持该模型：%s", detail).
			WithRetryable(true)
	case workbuddyBadRequestCodes[code]:
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "WorkBuddy 拒绝请求（%s）：%s", code, detail)
	case status >= 500:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "WorkBuddy 上游 %d：%s", status, detail).
			WithRetryable(true).WithStatus(http.StatusBadGateway)
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "WorkBuddy 拒绝请求：%s", detail)
	default:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "WorkBuddy 上游返回 %d：%s", status, detail)
	}
}

// wbCooldownRetry 取冷却映射中的时长作为 RetryAfter；无规则时退回 30 分钟
// （401/14015 都属于"过一会儿再看/重新登录"的临时状态，不是永久失效）。
func wbCooldownRetry(status int, body string) time.Duration {
	if d, _ := wbCooldown(status, body); d > 0 {
		return d
	}
	return 30 * time.Minute
}

type wbErrorPayload struct {
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Error   *wbErrorPayload `json:"error"`
}

func parseUpstreamError(body string) (code, message string) {
	var payload wbErrorPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return "", ""
	}
	code = jsonScalar(payload.Code)
	message = strings.TrimSpace(payload.Message)
	if payload.Error != nil {
		if code == "" {
			code = jsonScalar(payload.Error.Code)
		}
		if message == "" {
			message = strings.TrimSpace(payload.Error.Message)
		}
	}
	return code, message
}

func jsonScalar(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(raw))
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
