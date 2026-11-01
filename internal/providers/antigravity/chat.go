package antigravity

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
)

// ChatStream 把 IR 请求翻译成 Antigravity 的 Gemini 变体请求。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	token, err := p.ensureAccessToken(ctx, env)
	if err != nil {
		return nil, err
	}
	projectID, err := p.ensureProjectID(ctx, env, token)
	if err != nil {
		return nil, err
	}

	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = stripProviderPrefix(req.Model)
	}
	if model == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Antigravity 缺少模型名").WithProvider("antigravity")
	}

	payload := buildRequestBody(req, model, projectID)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Antigravity 请求序列化失败").WithCause(err)
	}

	resp, err := p.doRequest(ctx, env, token, body)
	if err != nil {
		return nil, err
	}
	// 401 可能是 token 刚过期：刷新一次再重试，避免让客户端看到一次无谓的失败。
	if resp.StatusCode == http.StatusUnauthorized && strings.TrimSpace(env.GetCredential("refresh_token")) != "" {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		if refreshErr := p.refreshAccessToken(ctx, env, env.GetCredential("refresh_token")); refreshErr == nil {
			resp, err = p.doRequest(ctx, env, env.GetCredential("access_token"), body)
			if err != nil {
				return nil, err
			}
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, mapUpstreamError(resp.StatusCode, string(raw), resp.Header)
	}

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		parseSSE(ctx, out, resp.Body)
	}()
	return out, nil
}

// baseURL 返回账号级上游地址；默认是官方 daily 端点。
// 账号显式填 base_url 时优先使用（企业版 / 自建代理 / 测试）。
func baseURL(env *provider.AccountEnv) string {
	if v := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/"); v != "" {
		return v
	}
	return dailyBaseURL
}

func (p *Provider) doRequest(ctx context.Context, env *provider.AccountEnv, token string, body []byte) (*http.Response, error) {
	endpoint := baseURL(env) + "/v1internal:streamGenerateContent?alt=sse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Antigravity 请求失败").WithCause(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("User-Agent", userAgent)

	client := env.Client()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Antigravity 上游连接失败").
			WithCause(err).WithProvider("antigravity").WithRetryable(true)
	}
	return resp, nil
}

func mapUpstreamError(status int, body string, hdr http.Header) *ir.APIError {
	msg := truncate(strings.TrimSpace(body), 400)
	if strings.Contains(body, "User location is not supported") {
		return ir.NewAPIError(ir.ErrCodeBadRequest,
			"Antigravity 拒绝了当前出口地区（User location is not supported）。请把该渠道的代理切到支持的地区后重试。 Original: "+msg).
			WithProvider("antigravity")
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Antigravity 鉴权失败（%d）：%s", status, msg).
			WithProvider("antigravity")
	case status == http.StatusTooManyRequests:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "Antigravity 限流：%s", msg).
			WithProvider("antigravity").WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, msg))
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "Antigravity 拒绝请求：%s", msg).
			WithProvider("antigravity")
	case status >= 500:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Antigravity 上游 %d：%s", status, msg).
			WithProvider("antigravity").WithRetryable(true).WithStatus(http.StatusBadGateway)
	default:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Antigravity 上游返回 %d：%s", status, msg).
			WithProvider("antigravity")
	}
}

// buildRequestBody 构造 v1internal 信封。
func buildRequestBody(req *ir.UnifiedChatRequest, model, projectID string) map[string]any {
	request := map[string]any{
		"contents":  toContents(req),
		"sessionId": stableSessionID(req),
	}
	if s := strings.TrimSpace(req.Instructions); s != "" {
		request["systemInstruction"] = map[string]any{
			"parts": []any{map[string]any{"text": s}},
		}
	}
	if tools := toGeminiTools(req.Tools); len(tools) > 0 {
		request["tools"] = tools
	}
	if cfg := toolConfig(req); cfg != nil {
		request["toolConfig"] = cfg
	}
	if gc := generationConfig(req, model); len(gc) > 0 {
		request["generationConfig"] = gc
	}

	return map[string]any{
		"model":       model,
		"userAgent":   "antigravity",
		"requestType": "agent",
		"project":     projectID,
		"requestId":   "agent-" + randomUUID(),
		"request":     request,
	}
}

type pendingThought struct {
	text      string
	signature string
}

func toContents(req *ir.UnifiedChatRequest) []any {
	var contents []any
	var parts []any
	var role string
	var thought pendingThought
	callNames := map[string]string{}

	flush := func() {
		if len(parts) == 0 {
			return
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
		parts = nil
	}
	switchRole := func(next string) {
		if role == "" {
			role = next
			return
		}
		if role != next {
			flush()
			role = next
		}
	}

	for _, item := range req.Items {
		switch item.Type {
		case ir.ItemTypeMessage:
			next := "model"
			if item.Role != "assistant" && item.Role != "model" {
				next = "user"
			}
			switchRole(next)
			if thought.text != "" && next == "model" {
				p := map[string]any{"text": thought.text, "thought": true}
				if thought.signature != "" {
					p["thoughtSignature"] = thought.signature
				}
				parts = append(parts, p)
				thought = pendingThought{}
			}
			if strings.TrimSpace(item.Text) != "" {
				parts = append(parts, map[string]any{"text": item.Text})
			}
			for _, att := range item.Attachments {
				if att.DataB64 == "" {
					if att.URL != "" {
						parts = append(parts, map[string]any{"text": "[附件] " + att.URL})
					}
					continue
				}
				mime := att.MIME
				if mime == "" {
					mime = "image/png"
				}
				parts = append(parts, map[string]any{
					"inlineData": map[string]any{"mimeType": mime, "data": att.DataB64},
				})
			}

		case ir.ItemTypeReasoning:
			if item.Text != "" {
				thought.text += item.Text
			}
			if item.ThoughtSignature != "" {
				thought.signature = item.ThoughtSignature
			}

		case ir.ItemTypeFunctionCall:
			switchRole("model")
			call := map[string]any{"name": item.Name}
			if strings.TrimSpace(item.Arguments) != "" {
				var args any
				if err := json.Unmarshal([]byte(item.Arguments), &args); err == nil {
					call["args"] = args
				} else {
					call["args"] = map[string]any{"_raw": item.Arguments}
				}
			} else {
				call["args"] = map[string]any{}
			}
			p := map[string]any{"functionCall": call}
			if thought.signature != "" {
				p["thoughtSignature"] = thought.signature
			}
			thought = pendingThought{}
			parts = append(parts, p)
			if item.CallID != "" {
				callNames[item.CallID] = item.Name
			}

		case ir.ItemTypeFunctionCallOutput:
			switchRole("user")
			name := callNames[item.CallID]
			if name == "" {
				name = item.Name
			}
			if name == "" {
				name = "tool"
			}
			parts = append(parts, map[string]any{
				"functionResponse": map[string]any{
					"name":     name,
					"response": map[string]any{"result": item.Output},
				},
			})
		}
	}
	flush()
	return contents
}

func toGeminiTools(tools []ir.UnifiedToolSpec) []any {
	if len(tools) == 0 {
		return nil
	}
	decls := make([]any, 0, len(tools))
	for _, t := range tools {
		decl := map[string]any{"name": t.Name}
		if t.Description != "" {
			decl["description"] = t.Description
		}
		var schema any
		if len(t.Parameters) > 0 {
			if err := json.Unmarshal(t.Parameters, &schema); err != nil {
				schema = nil
			}
		}
		decl["parameters"] = sanitizeSchema(schema)
		decls = append(decls, decl)
	}
	return []any{map[string]any{"functionDeclarations": decls}}
}

// sanitizeSchema 去掉 Gemini 不接受的 JSON Schema 关键字。
func sanitizeSchema(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			switch strings.ToLower(k) {
			case "$schema", "title", "default", "examples", "const", "additionalproperties",
				"$defs", "definitions", "$ref", "deprecated", "readonly", "writeonly", "$id", "$comment":
				continue
			}
			out[k] = sanitizeSchema(val)
		}
		if _, ok := out["type"]; !ok {
			if _, hasProps := out["properties"]; hasProps {
				out["type"] = "object"
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, val := range x {
			out = append(out, sanitizeSchema(val))
		}
		return out
	default:
		return v
	}
}

func generationConfig(req *ir.UnifiedChatRequest, model string) map[string]any {
	gc := map[string]any{"candidateCount": 1}
	if req.Temperature != nil {
		gc["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		gc["topP"] = *req.TopP
	}
	if len(req.StopSequences) > 0 {
		gc["stopSequences"] = req.StopSequences
	}
	if rf := req.ResponseFormat; rf != nil {
		switch rf.Type {
		case "json_object":
			gc["responseMimeType"] = "application/json"
		case "json_schema":
			gc["responseMimeType"] = "application/json"
			if len(rf.Schema) > 0 {
				var schema any
				if json.Unmarshal(rf.Schema, &schema) == nil {
					gc["responseSchema"] = schema
				}
			}
		}
	}
	// Gemini 侧对 maxOutputTokens 很敏感；只对 Claude 模型透传，其他走默认上限。
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 && strings.Contains(model, "claude") {
		gc["maxOutputTokens"] = *req.MaxOutputTokens
	}
	if len(gc) == 1 && gc["candidateCount"] == 1 {
		return gc
	}
	return gc
}

// toolConfig 把 IR 的工具选择策略映射到 Gemini 的 functionCallingConfig。
func toolConfig(req *ir.UnifiedChatRequest) map[string]any {
	if req.ToolChoice == nil {
		return nil
	}
	cfg := map[string]any{}
	switch req.ToolChoice.Mode {
	case "auto":
		cfg["mode"] = "AUTO"
	case "none":
		cfg["mode"] = "NONE"
	case "required":
		cfg["mode"] = "ANY"
	case "function":
		cfg["mode"] = "ANY"
		if req.ToolChoice.Name != "" {
			cfg["allowedFunctionNames"] = []string{req.ToolChoice.Name}
		}
	}
	if len(cfg) == 0 {
		return nil
	}
	return map[string]any{"functionCallingConfig": cfg}
}

func stableSessionID(req *ir.UnifiedChatRequest) string {
	if text := req.LastUserText(); text != "" {
		sum := sha256.Sum256([]byte(text))
		n := int64(binary.BigEndian.Uint64(sum[:8])) & 0x7FFFFFFFFFFFFFFF
		return fmt.Sprintf("-%d", n)
	}
	return "-" + randomDecimal()
}

func randomDecimal() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	n := int64(binary.BigEndian.Uint64(buf)) & 0x7FFFFFFFFFFFFFFF
	return fmt.Sprintf("%d", n)
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func stripProviderPrefix(model string) string {
	const prefix = "antigravity/"
	return strings.TrimPrefix(model, prefix)
}

// --- SSE 解析 ---

func parseSSE(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	parseSSEAs(ctx, out, r, "antigravity", "antigravity")
}

// parseSSEAs 解析 Gemini 形态的 SSE；providerName / callPrefix 让共用此解析器的
// 渠道（如 gemini-compat）在报错与工具调用 ID 上仍能标明自己。
func parseSSEAs(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader, providerName, callPrefix string) {
	started := false
	finish := ""
	sawTool := false
	toolSeq := 0
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
	started = true

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
		var root map[string]any
		if err := json.Unmarshal([]byte(data), &root); err != nil {
			continue
		}
		body := root
		if resp, ok := root["response"].(map[string]any); ok {
			body = resp
		}

		if errObj, ok := body["error"].(map[string]any); ok {
			emit(ir.UnifiedStreamEvent{
				Type: ir.EventError,
				Err: ir.NewAPIError(ir.ErrCodeUpstreamFailed,
					providerName+" 流内错误："+stringifyError(errObj)).WithProvider(providerName),
			})
			return
		}

		candidates, _ := body["candidates"].([]any)
		if len(candidates) > 0 {
			if cand, ok := candidates[0].(map[string]any); ok {
				if content, ok := cand["content"].(map[string]any); ok {
					parts, _ := content["parts"].([]any)
					for _, rawPart := range parts {
						part, ok := rawPart.(map[string]any)
						if !ok {
							continue
						}
						if text, ok := part["text"].(string); ok && text != "" {
							thought, _ := part["thought"].(bool)
							sig, _ := part["thoughtSignature"].(string)
							ev := ir.UnifiedStreamEvent{ThoughtSignature: sig}
							if thought {
								ev.Type = ir.EventReasoningDelta
							} else {
								ev.Type = ir.EventTextDelta
							}
							ev.TextDelta = text
							if !emit(ev) {
								return
							}
						}
						if call, ok := part["functionCall"].(map[string]any); ok {
							name, _ := call["name"].(string)
							args := "{}"
							if v, exists := call["args"]; exists {
								if b, err := json.Marshal(v); err == nil {
									args = string(b)
								}
							}
							toolSeq++
							id := fmt.Sprintf("%s_call_%d", callPrefix, toolSeq)
							sawTool = true
							if !emit(ir.UnifiedStreamEvent{
								Type: ir.EventToolCallStart, ToolCallID: id, ToolName: name,
							}) {
								return
							}
							if !emit(ir.UnifiedStreamEvent{
								Type: ir.EventToolCallDelta, ToolCallID: id, ArgsDelta: args,
							}) {
								return
							}
						}
					}
				}
				if fr, _ := cand["finishReason"].(string); strings.TrimSpace(fr) != "" {
					finish = mapFinishReason(fr)
				}
			}
		}

		if usage, ok := body["usageMetadata"].(map[string]any); ok {
			prompt := lookupInt(usage, "promptTokenCount")
			cached := lookupInt(usage, "cachedContentTokenCount")
			// Gemini 的 promptTokenCount 含 cachedContent；IR 口径要求拆开，
			// 否则命中缓存的输入会按原价计费。
			fresh := prompt - cached
			if fresh < 0 {
				fresh = 0
			}
			emit(ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: &ir.Usage{
				InputTokens:     fresh,
				OutputTokens:    lookupInt(usage, "candidatesTokenCount"),
				TotalTokens:     lookupInt(usage, "totalTokenCount"),
				CachedTokens:    cached,
				ReasoningTokens: lookupInt(usage, "thoughtsTokenCount"),
			}})
		}
	}

	if !started {
		return
	}
	// Gemini 在带工具调用时也可能报 STOP；对下游要翻译成 tool_calls，
	// 否则客户端不会进入工具执行分支。
	if sawTool && (finish == "" || finish == "stop") {
		finish = "tool_calls"
	}
	if finish == "" {
		finish = "stop"
	}
	emit(ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
}

func mapFinishReason(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return "content_filter"
	case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL":
		return "tool_calls"
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

func stringifyError(errObj map[string]any) string {
	msg, _ := errObj["message"].(string)
	code, _ := errObj["code"].(float64)
	if msg == "" {
		b, _ := json.Marshal(errObj)
		return string(b)
	}
	if code != 0 {
		return fmt.Sprintf("%.0f %s", code, msg)
	}
	return msg
}
