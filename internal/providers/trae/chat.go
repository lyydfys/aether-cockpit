package trae

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
)

// ChatStream 把 IR 请求翻译成 Trae 的 llm_utils_chat 请求并解析 SSE。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	token := accessToken(env)
	if token == "" {
		return nil, missingTokenError(p.id)
	}

	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = stripProviderPrefix(req.Model, p.id)
	}
	// auto 原本走 inline_chat，但上游当前对 inline_chat 的默认推理参数
	// （reasoning_effort=high + thinking=disabled）直接返回 3003；
	// chat_v3 + 具体模型是唯一实测可用的路径。
	if model == "" || strings.EqualFold(model, "auto") {
		model = p.defaultChatModel()
	}

	payload := buildRequestBody(req, model)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Trae 请求序列化失败").WithCause(err)
	}

	endpoint := p.baseURL(env) + chatPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Trae 请求失败").WithCause(err)
	}
	p.applyHeaders(httpReq.Header, env, token)

	client := env.Client()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Trae 上游连接失败").
			WithCause(err).WithProvider(p.id).WithRetryable(true)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, mapHTTPError(p.id, resp.StatusCode, string(raw), resp.Header)
	}

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		p.parseSSE(ctx, out, resp.Body)
	}()
	return out, nil
}

// applyHeaders 写入 Trae 官方客户端所需的完整请求头集合。
func (p *Provider) applyHeaders(h http.Header, env *provider.AccountEnv, token string) {
	traceID := randomHex(16)
	spanID := randomHex(8)
	requestID := randomUUID()

	deviceID := strings.TrimSpace(env.GetCredential("device_id"))
	if deviceID == "" {
		deviceID = hashDeviceID(env.GetCredential("machine_id"))
	}

	h.Set("Content-Type", "application/json")
	h.Set("Accept", "text/event-stream")
	// 上游对非常规 UA（尤其是 Go-http-client）会返回流内 4011；
	// 必须与 Trae 客户端保持一致，否则请求能送达但会被风控限流。
	h.Set("User-Agent", "Trae/"+p.ideVersion())
	h.Set("Authorization", "Cloud-IDE-JWT "+token)
	h.Set("X-Cloudide-Token", token)
	h.Set("x-app-id", appID)
	h.Set("x-app-version", appVersion)
	h.Set("x-ide-version-code", appVersionCode)
	h.Set("x-app-version-code", appVersionCode)
	h.Set("x-custom-trace-id", traceID)
	h.Set("x-flow-traceparent", "04-"+traceID+"-"+spanID+"-01")
	h.Set("x-device-brand", deviceBrand)
	h.Set("x-device-cpu", deviceCPU)
	h.Set("x-device-id", deviceID)
	h.Set("x-machine-id", strings.TrimSpace(env.GetCredential("machine_id")))
	h.Set("x-os-version", osVersion)
	h.Set("x-device-type", deviceType)
	h.Set("x-ide-version", p.ideVersion())
	h.Set("x-ide-version-type", "stable")
	h.Set("request-traffic-type", "prod")
	h.Set("x-uid", strings.TrimSpace(env.GetCredential("user_id")))
	h.Set("X-Request-ID", requestID)
	h.Set("X-Trae-Request-ID", requestID)
}

// buildRequestBody 构造 llm_utils_chat 请求体。
//
// 一律走 chat_v3 + model。上游不接受 config_name（部分模型会返回 4001），
// inline_chat 在当前服务端还会因默认推理参数冲突返回 3003。
func buildRequestBody(req *ir.UnifiedChatRequest, model string) map[string]any {
	body := map[string]any{
		"messages": buildMessages(req),
		"function": "chat_v3",
		"stream":   true,
		"model":    model,
	}
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		n := *req.MaxOutputTokens
		if n > 128000 {
			n = 128000
		}
		body["max_tokens"] = n
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if len(req.StopSequences) > 0 {
		body["stop"] = req.StopSequences
	}
	return body
}

// buildMessages 把 IR Items 压平成 Trae 的 [{role, content:[{type,text}]}]。
//
// Trae 没有原生 tools 字段，因此工具历史折叠为 <toolcall>/<tool_result> 文本；
// 有工具时在 system 消息里注入工具清单，行为与参考实现一致。
func buildMessages(req *ir.UnifiedChatRequest) []any {
	msgs := make([]any, 0, len(req.Items)+2)

	system := strings.TrimSpace(req.Instructions)
	if len(req.Tools) > 0 {
		protocol := toolProtocol(req.Tools)
		if system != "" {
			system += "\n\n" + protocol
		} else {
			system = protocol
		}
	}
	if system != "" {
		msgs = append(msgs, traeMessage("system", system))
	}

	for _, item := range req.Items {
		switch item.Type {
		case ir.ItemTypeMessage:
			if item.IsBlank() {
				continue
			}
			msgs = append(msgs, traeMessage(normalizeRole(item.Role), item.Text))

		case ir.ItemTypeReasoning:
			// Trae 只消费回复侧 reasoning；历史思考块没有可回放字段。

		case ir.ItemTypeFunctionCall:
			line := toolCallLine(item.Name, item.Arguments)
			if n := len(msgs); n > 0 {
				if last, ok := msgs[n-1].(map[string]any); ok && last["role"] == "assistant" {
					text := line
					if messageHasText(last) {
						text = "\n" + line
					}
					appendTextPart(last, text)
					continue
				}
			}
			msgs = append(msgs, traeMessage("assistant", line))

		case ir.ItemTypeFunctionCallOutput:
			out := fmt.Sprintf("<tool_result for=\"%s\">\n%s\n</tool_result>", item.CallID, item.Output)
			msgs = append(msgs, traeMessage("user", out))
		}
	}
	return msgs
}

// toolCallLine 生成参考实现使用的 <toolcall>{"name":...,"params":{...}}</toolcall>。
func toolCallLine(name, arguments string) string {
	var params any = map[string]any{}
	if s := strings.TrimSpace(arguments); s != "" {
		if err := json.Unmarshal([]byte(s), &params); err != nil {
			params = map[string]any{"_raw": s}
		}
	}
	encoded, err := json.Marshal(map[string]any{"name": name, "params": params})
	if err != nil {
		return "<toolcall></toolcall>"
	}
	return "<toolcall>" + string(encoded) + "</toolcall>"
}

// toolProtocol 生成注入 system 消息的工具清单（名称/描述/参数 JSON）。
func toolProtocol(tools []ir.UnifiedToolSpec) string {
	type descriptor struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		Parameters  any    `json:"parameters"`
	}
	list := make([]descriptor, 0, len(tools))
	for _, t := range tools {
		var params any = map[string]any{"type": "object", "properties": map[string]any{}}
		if len(t.Parameters) > 0 {
			var parsed any
			if err := json.Unmarshal(t.Parameters, &parsed); err == nil {
				params = parsed
			}
		}
		list = append(list, descriptor{Name: t.Name, Description: t.Description, Parameters: params})
	}
	blob, _ := json.Marshal(list)

	return "<toolcall_protocol>\n" +
		"When you need a tool, output ONLY this XML block (valid JSON inside):\n" +
		"<toolcall>{\"name\":\"ToolName\",\"params\":{\"arg\":\"value\"}}</toolcall>\n" +
		"Rules:\n" +
		"- JSON must have \"name\" and \"params\"\n" +
		"- Use EXACT tool names from the list (case-sensitive)\n" +
		"- Do not wrap toolcall in markdown fences\n" +
		fmt.Sprintf("Tools (%d) JSON:\n", len(list)) +
		string(blob) + "\n" +
		"</toolcall_protocol>"
}

func traeMessage(role, text string) map[string]any {
	return map[string]any{
		"role":    role,
		"content": []any{map[string]any{"type": "text", "text": text}},
	}
}

func appendTextPart(msg map[string]any, text string) {
	parts, _ := msg["content"].([]any)
	msg["content"] = append(parts, map[string]any{"type": "text", "text": text})
}

func messageHasText(msg map[string]any) bool {
	parts, _ := msg["content"].([]any)
	for _, raw := range parts {
		if part, ok := raw.(map[string]any); ok && anyString(part["text"]) != "" {
			return true
		}
	}
	return false
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "system", "developer":
		return "system"
	case "assistant", "model":
		return "assistant"
	default:
		return "user"
	}
}

func stripProviderPrefix(model, providerID string) string {
	prefix := providerID + "/"
	if strings.HasPrefix(model, prefix) {
		return strings.TrimPrefix(model, prefix)
	}
	return model
}

// --- SSE 解析 ---

// parseSSE 把 Trae 的具名事件流还原成 IR 极简事件。
func (p *Provider) parseSSE(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	if !emitEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventStart}) {
		return
	}

	dec := wirecodec.NewSSEDecoder(r)
	finish := ""
	sawTool := false
	toolSeq := 0
	toolStarted := map[string]bool{}

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

		event := strings.ToLower(strings.TrimSpace(frame.Event))
		switch event {
		case "queue_begin", "request_wait_in_queue", "queue_end":
			// 排队事件对下游无意义，必须忽略。
			continue

		case "token_usage":
			if !emitUsagePayload(ctx, out, data) {
				return
			}

		case "error":
			emitEvent(ctx, out, ir.UnifiedStreamEvent{
				Type: ir.EventError,
				Err:  traeStreamError(p.id, data),
			})
			return

		case "done":
			if v := finishReasonFromData(data); v != "" {
				finish = v
			}

		case "output", "":
			if !p.handleOutputPayload(ctx, out, data, &toolSeq, toolStarted, &sawTool) {
				return
			}
		}
	}

	if finish == "" {
		if sawTool {
			finish = "tool_calls"
		} else {
			finish = "stop"
		}
	} else {
		finish = normalizeFinish(finish)
	}
	emitEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
}

// handleOutputPayload 处理一条 event=output 的增量。
// 返回 false 表示已终止（错误事件或 ctx 取消）。
func (p *Provider) handleOutputPayload(
	ctx context.Context,
	out chan<- ir.UnifiedStreamEvent,
	data string,
	toolSeq *int,
	toolStarted map[string]bool,
	sawTool *bool,
) bool {
	var root map[string]any
	if err := json.Unmarshal([]byte(data), &root); err != nil {
		return true
	}

	if errObj, ok := root["error"].(map[string]any); ok {
		code := anyCode(errObj["code"])
		msg := anyString(errObj["message"])
		// 流内错误：只有业务码与文案，没有 HTTP 响应头。
		err := traeCodeError(p.id, code, msg, nil)
		if err == nil {
			err = ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Trae 流内错误："+msg).WithProvider(p.id)
		}
		emitEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventError, Err: err})
		return false
	}

	if reasoning := firstNonEmpty(rawString(root["reasoning"]), rawString(root["reasoning_content"])); reasoning != "" {
		if !emitEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: reasoning}) {
			return false
		}
	}

	content := rawString(root["content"])
	if response := rawString(root["response"]); response != "" && !isProgressText(response) {
		content += response
	}
	if content != "" {
		if !emitEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: content}) {
			return false
		}
	}

	calls, _ := root["tool_calls"].([]any)
	for _, raw := range calls {
		call, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := callName(call)
		id := anyString(call["id"])
		if id == "" {
			*toolSeq = *toolSeq + 1
			id = fmt.Sprintf("trae_call_%d", *toolSeq)
		}
		*sawTool = true

		if !toolStarted[id] {
			toolStarted[id] = true
			if !emitEvent(ctx, out, ir.UnifiedStreamEvent{
				Type: ir.EventToolCallStart, ToolCallID: id, ToolName: name,
			}) {
				return false
			}
		}
		if args := toolArguments(call); args != "" {
			if !emitEvent(ctx, out, ir.UnifiedStreamEvent{
				Type: ir.EventToolCallDelta, ToolCallID: id, ArgsDelta: args,
			}) {
				return false
			}
		}
	}
	return true
}

func callName(call map[string]any) string {
	if name := anyString(call["name"]); name != "" {
		return name
	}
	if name := anyString(call["tool_name"]); name != "" {
		return name
	}
	if fn, ok := call["function"].(map[string]any); ok {
		return anyString(fn["name"])
	}
	return ""
}

func toolArguments(call map[string]any) string {
	var value any
	switch {
	case call["params"] != nil:
		value = call["params"]
	case call["arguments"] != nil:
		value = call["arguments"]
	case call["input"] != nil:
		value = call["input"]
	case call["function"] != nil:
		if fn, ok := call["function"].(map[string]any); ok {
			value = fn["arguments"]
		}
	}
	if value == nil {
		return "{}"
	}
	if s, ok := value.(string); ok {
		if strings.TrimSpace(s) == "" {
			return "{}"
		}
		return s
	}
	b, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// finishReasonFromData 兼容 finish_reason / finishReason 以及 data 包裹形态。
func finishReasonFromData(data string) string {
	var root map[string]any
	if err := json.Unmarshal([]byte(data), &root); err != nil {
		return ""
	}
	body := root
	if nested, ok := root["data"].(map[string]any); ok {
		body = nested
	}
	return firstNonEmpty(
		anyString(body["finish_reason"]),
		anyString(body["finishReason"]),
		anyString(root["finish_reason"]),
		anyString(root["finishReason"]),
	)
}

func normalizeFinish(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "stop", "":
		return "stop"
	case "length", "max_tokens":
		return "length"
	case "tool_calls", "tool_call", "function_call":
		return "tool_calls"
	case "content_filter":
		return "content_filter"
	default:
		return "stop"
	}
}

// emitUsagePayload 灵活读取 token_usage 事件的多种字段命名。
func emitUsagePayload(ctx context.Context, out chan<- ir.UnifiedStreamEvent, data string) bool {
	var root map[string]any
	if err := json.Unmarshal([]byte(data), &root); err != nil {
		return true
	}
	usage := root
	if nested, ok := root["data"].(map[string]any); ok {
		usage = nested
	}
	if nested, ok := usage["usage"].(map[string]any); ok {
		usage = nested
	}

	in := firstIntIn(usage, "prompt_tokens", "promptTokens", "input_tokens", "inputTokens")
	outTokens := firstIntIn(usage, "completion_tokens", "completionTokens", "output_tokens", "outputTokens")
	total := firstIntIn(usage, "total_tokens", "totalTokens")
	if in == 0 && outTokens == 0 && total == 0 {
		return true
	}
	if total == 0 {
		total = in + outTokens
	}
	return emitEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: &ir.Usage{
		InputTokens:  in,
		OutputTokens: outTokens,
		TotalTokens:  total,
		CachedTokens: firstIntIn(usage, "cached_tokens", "cachedTokens"),
	}})
}

func firstIntIn(m map[string]any, keys ...string) int {
	for _, key := range keys {
		if n := anyInt(m[key]); n != 0 {
			return n
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// isProgressText 跳过 Trae 的 prompt 构建进度行。
func isProgressText(s string) bool {
	return strings.HasPrefix(s, "Building prompt:") ||
		strings.HasPrefix(s, "Completed building prompt")
}

func emitEvent(ctx context.Context, out chan<- ir.UnifiedStreamEvent, ev ir.UnifiedStreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// --- 错误映射 ---

// traeStreamError 把 event=error 的载荷翻译成 IR 错误。
// 流内错误发生在 200 的流里，拿不到限流响应头，只能让文案兜底。
func traeStreamError(providerID, data string) error {
	var root map[string]any
	if err := json.Unmarshal([]byte(data), &root); err != nil {
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Trae 流内错误：%s", truncate(data, 300)).
			WithProvider(providerID)
	}
	code := anyCode(root["code"])
	msg := anyString(root["message"])
	if err := traeCodeError(providerID, code, msg, nil); err != nil {
		return err
	}
	return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Trae 流内错误（%d）：%s", code, msg).
		WithProvider(providerID)
}

// traeCodeError 映射 Trae 的业务错误码；未知码返回 nil。
func traeCodeError(providerID string, code int, message string, hdr http.Header) *ir.APIError {
	label := fmt.Sprintf("%d %s", code, strings.TrimSpace(message))
	switch code {
	case 1001:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Trae 鉴权失败：%s", label).
			WithProvider(providerID)
	case 4008:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Trae 额度已耗尽：%s", label).
			WithProvider(providerID)
	case 4011:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "Trae 限流：%s", label).
			WithProvider(providerID).WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, label))
	case 4017:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "Trae 触发风控/并发限制：%s", label).
			WithProvider(providerID).WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, label))
	}
	return nil
}

// mapHTTPError 优先解析上游 JSON 业务码，再退化为 HTTP 状态码映射。
func mapHTTPError(providerID string, status int, raw string, hdr http.Header) *ir.APIError {
	body := strings.TrimSpace(raw)
	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err == nil {
		if nested, ok := root["error"].(map[string]any); ok {
			root = nested
		}
		code := anyCode(root["code"])
		msg := anyString(root["message"])
		if code != 0 {
			if ae := traeCodeError(providerID, code, msg, hdr); ae != nil {
				return ae
			}
		}
		if msg != "" {
			body = msg
		}
	}

	msg := truncate(body, 400)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Trae 鉴权失败（%d）：%s", status, msg).
			WithProvider(providerID)
	case status == http.StatusTooManyRequests:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "Trae 限流（%d）：%s", status, msg).
			WithProvider(providerID).WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, msg))
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "Trae 拒绝请求（%d）：%s", status, msg).
			WithProvider(providerID)
	case status >= 500:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Trae 上游 %d：%s", status, msg).
			WithProvider(providerID).WithRetryable(true).WithStatus(http.StatusBadGateway)
	default:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Trae 上游返回 %d：%s", status, msg).
			WithProvider(providerID)
	}
}
