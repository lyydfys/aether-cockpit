package workbuddy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
)

const (
	userAgent  = "WorkBuddy/5.5.6"
	ideVersion = "5.8.3"
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
	var resp *http.Response
	// 最多两次：第一次用会话哈希出的 id，被 11128 拦下时换一组全新 id 再试。
	// 上游的 displayMsg 是"请求被拦截，请重新发送"，但它按会话/请求 id 记住
	// 被拦的那一次，原样重发还是同一条 id，只会再被拦——所以必须换 id。
	for attempt := 0; attempt < 2; attempt++ {
		httpReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost,
			p.baseURL(env)+"/chat/completions", bytes.NewReader(body))
		if reqErr != nil {
			return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 WorkBuddy 请求失败").WithCause(reqErr)
		}
		p.applyHeaders(httpReq, env, token, userID, req, attempt > 0)

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
		if attempt == 0 && channelBlocked(string(raw)) {
			continue
		}
		if riskBlocked(status, string(raw)) {
			// 风控信号：按下暂停键，让整个渠道冷静一段时间；继续重试只会让
			// "可疑"升级成"确认滥用"，还可能连坐同区域其它账号。
			pauseProvider(p.profile.id, riskPauseDefault)
			return nil, ir.NewAPIError(ir.ErrCodeUpstreamRateLimit,
				"上游判定为异常行为（风控），已暂停该渠道 10 分钟；请优先使用官方客户端，并避免多账号共用同一出口").
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

// applyHeaders 写入 WorkBuddy 客户端要求的自定义头。
func (p *Provider) applyHeaders(req *http.Request, env *provider.AccountEnv, token, userID string, chat *ir.UnifiedChatRequest, fresh bool) {
	conversationID, requestID, messageID := conversationIDs(chat, fresh)

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-User-Id", userID)
	// 设备风控头：官方桌面客户端登录后会拿到设备令牌，后续调用都带着它。
	// 这个令牌由服务端签发，客户端无法凭空生成，所以只在账号凭证里配置了
	// 才发送（device_token / device_id 两个字段，可从官方客户端登录态里取）。
	//
	// 为什么不能自己编一个：伪造的令牌比"没有令牌"更像异常样本，反而提高
	// 被风控的概率。宁可缺，不可编。
	if v := strings.TrimSpace(env.GetCredential("device_token")); v != "" {
		req.Header.Set("X-Device-Token", v)
	}
	if v := strings.TrimSpace(env.GetCredential("device_id")); v != "" {
		req.Header.Set("X-Device-Id", v)
	}
	req.Header.Set("X-Domain", p.domain(env))
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("X-IDE-Type", "WorkBuddy")
	// User-Agent 必须保留 WorkBuddy 前缀，否则上游返回 10085。
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Agent-Purpose", "conversation")
	req.Header.Set("X-IDE-Name", "WorkBuddy")
	req.Header.Set("X-IDE-Version", ideVersion)
	req.Header.Set("X-Agent-Intent", "craft")
	req.Header.Set("X-Requested-With", "WorkBuddy")
	req.Header.Set("X-Conversation-ID", conversationID)
	req.Header.Set("X-Conversation-Request-ID", requestID)
	req.Header.Set("X-Conversation-Message-ID", messageID)
	req.Header.Set("X-Request-ID", randomUUID())

	if v := strings.TrimSpace(env.GetCredential("enterprise_id")); v != "" {
		req.Header.Set("X-Enterprise-Id", v)
	}
	if v := strings.TrimSpace(env.GetCredential("tenant_id")); v != "" {
		req.Header.Set("X-Tenant-Id", v)
	}
	if p.profile.intl {
		req.Header.Set("X-Agent-Type", "main")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
}

// conversationIDs 生成稳定的会话/发送/消息标识。
//
// X-Conversation-Request-ID 对同一次逻辑发送必须稳定（重试、工具续轮看起来像
// 同一个请求），因此用会话 + 最近一条用户文本做哈希；X-Request-ID 是每次
// HTTP 请求唯一的随机 uuid。
//
// fresh=true 时三个 id 全部随机：通道校验（11128）按 id 记住被拦的那一次，
// 原样重发同一条 id 必然再被拦，只能换一组新 id 重放。
func conversationIDs(req *ir.UnifiedChatRequest, fresh bool) (conversationID, requestID, messageID string) {
	if fresh {
		return randomUUID(), randomUUID(), randomUUID()
	}
	lastText := req.LastUserText()
	seed := strings.TrimSpace(req.SessionKey)
	if seed != "" {
		seed = "session:" + seed
	} else if first, last := firstUserText(req), lastText; first != "" || last != "" {
		seed = "text:" + first + "|" + last
	}

	if seed == "" {
		conversationID = randomUUID()
	} else {
		conversationID = stableID(seed)
	}

	if lastText != "" {
		requestID = stableID(conversationID + "|request|" + lastText)
	} else if seed != "" {
		requestID = stableID(seed + "|request")
	} else {
		requestID = randomUUID()
	}

	turns := 0
	for _, item := range req.Items {
		if item.IsUserTurn() {
			turns++
		}
	}
	if turns == 0 {
		turns = 1
	}
	messageID = stableID(conversationID + "|message|" + strconv.Itoa(turns) + "|" + lastText)
	return conversationID, requestID, messageID
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
		payload["reasoning_effort"] = req.ReasoningEffort
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

func stableID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:16])
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
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
var workbuddyBadRequestCodes = map[string]bool{
	"10085": true,
	"11128": true,
	"11133": true,
	"11153": true,
	"11140": true,
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
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
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
