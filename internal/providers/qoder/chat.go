package qoder

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
)

const chatPath = "/algo/api/v2/service/pro/sse/agent_chat_generation?FetchKeys=llm_model_result&AgentId=agent_common&Encode=1"

// ChatStream 发起 Qoder agent_chat_generation 请求。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	c, err := p.ensureToken(ctx, env)
	if err != nil {
		return nil, err
	}

	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = stripPrefix(req.Model, p.profile.id)
	}
	if model == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Qoder 缺少模型名").WithProvider(p.profile.id)
	}
	// 旧静态目录里的显示名映射到上游 key；实时目录本身已带 key。
	model = legacyModelKey(model)

	payload := buildPayload(req, model)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Qoder 请求序列化失败").WithCause(err)
	}

	resp, err := p.send(ctx, env, body, c)
	if err != nil {
		return nil, err
	}
	// 401/403 时尝试刷新一次设备令牌，再重建签名重试。
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		resp.Body.Close()
		if strings.TrimSpace(env.GetCredential("refresh_token")) != "" {
			if refreshErr := p.refreshToken(ctx, env); refreshErr == nil {
				c = p.credentials(env)
				resp, err = p.send(ctx, env, body, c)
				if err != nil {
					return nil, err
				}
			}
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			raw2, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			resp.Body.Close()
			msg := string(raw2)
			if strings.TrimSpace(msg) == "" {
				msg = string(raw)
			}
			return nil, mapHTTPError(resp.StatusCode, msg, resp.Header).WithProvider(p.profile.id)
		}
	}
	// 429 + "All models failed"：qoder 后端池抖动（实测稍后重试即成功），
	// 在 provider 内部重试两次，别把瞬时 429 直接抛给用户。
	for attempt := 0; resp.StatusCode == http.StatusTooManyRequests && attempt < 2; attempt++ {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		resp.Body.Close()
		if !strings.Contains(string(raw), "All models failed") {
			return nil, mapHTTPError(resp.StatusCode, string(raw), resp.Header).WithProvider(p.profile.id)
		}
		select {
		case <-ctx.Done():
			return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "客户端已取消").WithCause(ctx.Err())
		case <-time.After(time.Duration(attempt+1) * 300 * time.Millisecond):
		}
		resp, err = p.send(ctx, env, body, c)
		if err != nil {
			return nil, err
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
		resp.Body.Close()
		return nil, mapHTTPError(resp.StatusCode, string(raw), resp.Header).WithProvider(p.profile.id)
	}

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		parseSSE(ctx, out, resp.Body)
	}()
	return out, nil
}

func (p *Provider) send(ctx context.Context, env *provider.AccountEnv, body []byte, c creds) (*http.Response, error) {
	wire := encodeQoderBody(body)
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = p.profile.baseURL
	}
	requestURL := base + chatPath
	headers, err := p.buildCOSYHeaders(wire, requestURL, c)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(wire))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Qoder 请求失败").WithCause(err)
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	return p.doHTTP(env, httpReq)
}

func mapHTTPError(status int, body string, hdr http.Header) *ir.APIError {
	msg := truncate(body, 300)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Qoder 鉴权失败（%d）：%s", status, msg)
	case status == http.StatusTooManyRequests:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "Qoder 限流：%s", msg).
			WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, msg))
	case status >= 500:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Qoder 上游 %d：%s", status, msg).
			WithRetryable(true).WithStatus(http.StatusBadGateway)
	// 账号额度耗尽时 Qoder 上游不给 402/429，而是 400 + agent_router
	// "flow nodes found"（2026-11-01 实测）。归到额度耗尽，别让用户追假 400。
	case status == http.StatusBadRequest && qoderQuotaExhausted(msg):
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit,
			"Qoder 账号额度已用尽（上游 400：%s）", msg).
			WithStatus(http.StatusTooManyRequests)
	default:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Qoder 上游返回 %d：%s", status, msg)
	}
}

// qoderQuotaExhausted 识别"额度耗尽"的上游文案。
func qoderQuotaExhausted(body string) bool {
	return strings.Contains(strings.ToLower(body), "flow nodes")
}

func buildPayload(req *ir.UnifiedChatRequest, model string) map[string]any {
	sessionID := stableSessionID(req)
	payload := map[string]any{
		"model":               model,
		"messages":            toMessages(req),
		"stream":              true,
		"stream_options":      map[string]any{"include_usage": true},
		"enable_thinking":     true,
		"preserve_thinking":   true,
		"parallel_tool_calls": true,
		"prompt_cache_key":    sessionID,
		"metadata": map[string]any{
			"context": map[string]any{
				"request_id":        randomUUID(),
				"request_set_id":    randomUUID(),
				"session_id":        sessionID,
				"source_session_id": sessionID,
				"os_type":           "windows",
				"task_id":           "common",
				"client_type":       "10",
			},
		},
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		payload["top_p"] = *req.TopP
	}
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		payload["max_tokens"] = *req.MaxOutputTokens
	}
	if len(req.StopSequences) > 0 {
		payload["stop"] = req.StopSequences
	}
	if len(req.Tools) > 0 {
		payload["tools"] = toTools(req.Tools)
	}
	return payload
}

func toMessages(req *ir.UnifiedChatRequest) []map[string]any {
	out := make([]map[string]any, 0, len(req.Items)+1)
	if strings.TrimSpace(req.Instructions) != "" {
		out = append(out, map[string]any{"role": "system", "content": req.Instructions})
	}

	var (
		pendingCalls     []map[string]any
		pendingReasoning strings.Builder
	)
	flushAssistant := func() {
		if len(pendingCalls) == 0 && pendingReasoning.Len() == 0 {
			return
		}
		msg := map[string]any{"role": "assistant", "content": " "}
		if len(pendingCalls) > 0 {
			msg["tool_calls"] = pendingCalls
		}
		if pendingReasoning.Len() > 0 {
			msg["reasoning_content"] = pendingReasoning.String()
		}
		out = append(out, msg)
		pendingCalls = nil
		pendingReasoning.Reset()
	}

	for _, item := range req.Items {
		switch item.Type {
		case ir.ItemTypeMessage:
			flushAssistant()
			role := "user"
			if item.Role == "assistant" || item.Role == "model" {
				role = "assistant"
			}
			content := item.Text
			if len(item.Attachments) > 0 {
				content = "" // 附件暂不支持，保持文本可用
			}
			out = append(out, map[string]any{"role": role, "content": content})

		case ir.ItemTypeReasoning:
			pendingReasoning.WriteString(item.Text)

		case ir.ItemTypeFunctionCall:
			args := strings.TrimSpace(item.Arguments)
			if args == "" {
				args = "{}"
			}
			pendingCalls = append(pendingCalls, map[string]any{
				"id":   item.CallID,
				"type": "function",
				"function": map[string]any{
					"name":      item.Name,
					"arguments": args,
				},
			})

		case ir.ItemTypeFunctionCallOutput:
			flushAssistant()
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": item.CallID,
				"content":      item.Output,
			})
		}
	}
	flushAssistant()
	return out
}

func toTools(tools []ir.UnifiedToolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		fn := map[string]any{"name": t.Name}
		if t.Description != "" {
			fn["description"] = t.Description
		}
		if len(t.Parameters) > 0 {
			var schema any
			if err := json.Unmarshal(t.Parameters, &schema); err == nil {
				fn["parameters"] = schema
			}
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

func stableSessionID(req *ir.UnifiedChatRequest) string {
	if text := req.LastUserText(); text != "" {
		sum := sha256.Sum256([]byte(text))
		n := int64(binary.BigEndian.Uint64(sum[:8])) & 0x7FFFFFFFFFFFFFFF
		return fmt.Sprintf("-%d", n)
	}
	return "-" + fmt.Sprintf("%d", time.Now().UnixNano())
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func stripPrefix(model, providerID string) string {
	return strings.TrimPrefix(model, providerID+"/")
}

// --- SSE 解析 ---

type envelope struct {
	StatusCodeValue int    `json:"statusCodeValue"`
	Body            string `json:"body"`
}

func parseSSE(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	emit := func(ev ir.UnifiedStreamEvent) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if !emit(ir.UnifiedStreamEvent{Type: ir.EventStart}) {
		return
	}

	finish := ""
	sawTool := false
	toolSeq := 0
	toolIDs := map[int]string{}

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
		if data == "[DONE]" {
			break
		}

		var env envelope
		if err := json.Unmarshal([]byte(data), &env); err != nil {
			// 少数网关直接返回裸 chunk。
			if handleChunk(data, emit, &finish, &sawTool, &toolSeq, toolIDs) {
				break
			}
			continue
		}
		if env.StatusCodeValue != 0 && env.StatusCodeValue != 200 {
			// 流内 envelope：外层已是 200，拿不到限流响应头，
			// 只能让 RetryAfterHint 从 envelope.body 文案里取提示。
			emit(ir.UnifiedStreamEvent{
				Type: ir.EventError,
				Err:  mapHTTPError(env.StatusCodeValue, env.Body, nil),
			})
			return
		}
		body := strings.TrimSpace(env.Body)
		if body == "" {
			continue
		}
		if body == "[DONE]" {
			break
		}
		if handleChunk(body, emit, &finish, &sawTool, &toolSeq, toolIDs) {
			break
		}
	}

	if sawTool && (finish == "" || finish == "stop") {
		finish = "tool_calls"
	}
	if finish == "" {
		finish = "stop"
	}
	emit(ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
}

func handleChunk(data string, emit func(ir.UnifiedStreamEvent) bool, finish *string, sawTool *bool, toolSeq *int, toolIDs map[int]string) bool {
	var chunk map[string]any
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return false
	}
	if errObj, ok := chunk["error"].(map[string]any); ok {
		emit(ir.UnifiedStreamEvent{Type: ir.EventError,
			Err: ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Qoder 流内错误："+stringifyError(errObj))})
		return true
	}
	if code, ok := chunk["code"].(float64); ok && code != 0 {
		if msg, _ := chunk["message"].(string); msg != "" {
			emit(ir.UnifiedStreamEvent{Type: ir.EventError,
				Err: ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Qoder 流内错误："+msg)})
			return true
		}
	}

	if choices, ok := chunk["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if delta, ok := choice["delta"].(map[string]any); ok {
				if reasoning, ok := delta["reasoning_content"].(string); ok && reasoning != "" {
					if !emit(ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: reasoning}) {
						return true
					}
				}
				if content, ok := delta["content"].(string); ok && content != "" {
					if !emit(ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: content}) {
						return true
					}
				}
				if calls, ok := delta["tool_calls"].([]any); ok {
					for _, raw := range calls {
						call, ok := raw.(map[string]any)
						if !ok {
							continue
						}
						index := 0
						if v, ok := call["index"].(float64); ok {
							index = int(v)
						}
						id, _ := call["id"].(string)
						if id == "" {
							id = toolIDs[index]
						}
						name := ""
						args := ""
						if fn, ok := call["function"].(map[string]any); ok {
							name, _ = fn["name"].(string)
							args, _ = fn["arguments"].(string)
						}
						if id == "" {
							*toolSeq = *toolSeq + 1
							id = fmt.Sprintf("qoder_call_%d", *toolSeq)
						}
						if name != "" {
							*sawTool = true
							toolIDs[index] = id
							if !emit(ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: id, ToolName: name}) {
								return true
							}
						}
						if args != "" {
							if toolIDs[index] == "" {
								toolIDs[index] = id
							}
							if !emit(ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ToolCallID: toolIDs[index], ArgsDelta: args}) {
								return true
							}
						}
					}
				}
			}
			if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
				*finish = mapFinishReason(fr)
			}
		}
	}

	if usage, ok := chunk["usage"].(map[string]any); ok {
		emit(ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: &ir.Usage{
			InputTokens:     lookupInt(usage, "prompt_tokens"),
			OutputTokens:    lookupInt(usage, "completion_tokens"),
			TotalTokens:     lookupInt(usage, "total_tokens"),
			CachedTokens:    lookupNestedInt(usage, "prompt_tokens_details", "cached_tokens"),
			ReasoningTokens: lookupNestedInt(usage, "completion_tokens_details", "reasoning_tokens"),
		}})
	}
	return false
}

func mapFinishReason(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "stop":
		return "stop"
	case "length", "max_tokens":
		return "length"
	case "tool_calls", "function_call":
		return "tool_calls"
	case "content_filter", "sensitive":
		return "content_filter"
	default:
		return "stop"
	}
}

func lookupInt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

func lookupNestedInt(m map[string]any, parent, key string) int {
	if p, ok := m[parent].(map[string]any); ok {
		return lookupInt(p, key)
	}
	return 0
}

func stringifyError(errObj map[string]any) string {
	if msg, _ := errObj["message"].(string); msg != "" {
		return msg
	}
	b, _ := json.Marshal(errObj)
	return string(b)
}
