package kiro

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
)

const defaultRegion = "us-east-1"

// ChatStream 发起 Kiro generateAssistantResponse 请求。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	token := strings.TrimSpace(firstNonEmpty(env.GetCredential("access_token"), env.GetCredential("token")))
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Kiro 账号缺少 access_token；请手动填写或从 Kiro CLI 登录态导入").
			WithProvider("kiro")
	}
	profileArn := strings.TrimSpace(env.GetCredential("profile_arn"))
	if profileArn == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Kiro 账号缺少 profile_arn；Kiro Runtime 必需").
			WithProvider("kiro")
	}
	region := strings.TrimSpace(env.GetCredential("region"))
	if r := regionFromProfileArn(profileArn); r != "" {
		region = r
	}
	if region == "" {
		region = defaultRegion
	}

	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = strings.TrimPrefix(req.Model, "kiro/")
	}
	if model == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Kiro 缺少模型名").WithProvider("kiro")
	}

	payload := buildPayload(req, model, profileArn)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Kiro 请求序列化失败").WithCause(err)
	}

	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = "https://runtime." + region + ".kiro.dev"
	}
	requestURL := base + "/generateAssistantResponse"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Kiro 请求失败").WithCause(err)
	}
	ua := "aws-sdk-rust/1.0.0 ua/2.1 os/other lang/rust api/codewhispererstreaming#1.28.3 m/E app/AmazonQ-For-CLI md/appVersion-1.28.3-" + randomHex(8)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/vnd.amazon.eventstream")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	if strings.HasPrefix(token, "ksk_") {
		httpReq.Header.Set("tokentype", "API_KEY")
	}
	httpReq.Header.Set("x-amzn-codewhisperer-optout", "true")
	httpReq.Header.Set("x-amzn-kiro-agent-mode", "vibe")
	httpReq.Header.Set("x-amz-user-agent", ua)
	httpReq.Header.Set("User-Agent", ua)
	httpReq.Header.Set("amz-sdk-invocation-id", randomUUID())
	httpReq.Header.Set("amz-sdk-request", "attempt=1; max=3")

	client := env.Client()
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Kiro 上游连接失败").
			WithCause(err).WithProvider("kiro").WithRetryable(true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, mapHTTPError(resp.StatusCode, string(raw), resp.Header)
	}

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		parseEventStream(ctx, out, resp.Body)
	}()
	return out, nil
}

func buildPayload(req *ir.UnifiedChatRequest, model, profileArn string) map[string]any {
	current, history := buildConversation(req, model)
	state := map[string]any{
		"chatTriggerType": "MANUAL",
		"conversationId":  stableConversationID(req),
		"currentMessage":  current,
	}
	if len(history) > 0 {
		state["history"] = history
	}
	return map[string]any{
		"conversationState": state,
		"profileArn":        profileArn,
	}
}

// buildConversation 生成 Kiro 的 currentMessage 与 history。
func buildConversation(req *ir.UnifiedChatRequest, model string) (map[string]any, []any) {
	type turn struct {
		user    bool
		msg     map[string]any
		content strings.Builder
	}
	var turns []*turn
	systemPrefix := strings.TrimSpace(req.Instructions)

	appendTurn := func(user bool, content string) *turn {
		if user && systemPrefix != "" {
			if content != "" {
				content = "System: " + systemPrefix + "\n\n" + content
			} else {
				content = "System: " + systemPrefix
			}
			systemPrefix = ""
		}
		if len(turns) > 0 {
			last := turns[len(turns)-1]
			if last.user == user {
				if content != "" {
					if last.content.Len() > 0 {
						last.content.WriteString("\n\n")
					}
					last.content.WriteString(content)
					// 保留已有的 toolResults/tools。
					last.msg = turnMessageWithContext(user, last.content.String(), last.msg)
				}
				return last
			}
		}
		t := &turn{user: user, content: strings.Builder{}}
		if content != "" {
			t.content.WriteString(content)
		}
		t.msg = turnMessage(user, t.content.String())
		turns = append(turns, t)
		return t
	}

	for _, item := range req.Items {
		switch item.Type {
		case ir.ItemTypeMessage:
			if item.Role == "assistant" || item.Role == "model" {
				appendTurn(false, item.Text)
			} else {
				appendTurn(true, item.Text)
			}
		case ir.ItemTypeReasoning:
			// 历史思考不写入 Kiro，避免伪造 assistant 内容。
		case ir.ItemTypeFunctionCall:
			t := appendTurn(false, "")
			t.msg["assistantResponseMessage"].(map[string]any)["toolUses"] = appendTools(t.msg, item)
		case ir.ItemTypeFunctionCallOutput:
			t := appendTurn(true, "")
			userMsg := t.msg["userInputMessage"].(map[string]any)
			ctxMap, _ := userMsg["userInputMessageContext"].(map[string]any)
			if ctxMap == nil {
				ctxMap = map[string]any{}
				userMsg["userInputMessageContext"] = ctxMap
			}
			results, _ := ctxMap["toolResults"].([]any)
			results = append(results, map[string]any{
				"toolUseId": item.CallID,
				"content":   []any{map[string]any{"text": item.Output}},
				"status":    "success",
			})
			ctxMap["toolResults"] = results
		}
	}

	// 工具声明挂到 currentMessage。
	if len(turns) == 0 {
		turns = append(turns, appendTurn(true, ""))
	}
	if !turns[len(turns)-1].user {
		turns = append(turns, appendTurn(true, ""))
	}
	current := turns[len(turns)-1]
	history := make([]any, 0, len(turns)-1)
	for _, t := range turns[:len(turns)-1] {
		history = append(history, t.msg)
	}
	if len(req.Tools) > 0 {
		userMsg, ok := current.msg["userInputMessage"].(map[string]any)
		if ok {
			ctxMap, _ := userMsg["userInputMessageContext"].(map[string]any)
			if ctxMap == nil {
				ctxMap = map[string]any{}
				userMsg["userInputMessageContext"] = ctxMap
			}
			ctxMap["tools"] = toKiroTools(req.Tools)
		}
	}
	return current.msg, history
}

func turnMessage(user bool, content string) map[string]any {
	if user {
		return map[string]any{"userInputMessage": map[string]any{"content": content}}
	}
	return map[string]any{"assistantResponseMessage": map[string]any{"content": content}}
}

// turnMessageWithContext 在合并相邻 user 轮次时保留已有的 toolResults。
func turnMessageWithContext(user bool, content string, old map[string]any) map[string]any {
	if !user {
		return turnMessage(user, content)
	}
	msg := turnMessage(true, content)
	if oldUser, ok := old["userInputMessage"].(map[string]any); ok {
		if ctxMap, ok := oldUser["userInputMessageContext"].(map[string]any); ok {
			msg["userInputMessage"].(map[string]any)["userInputMessageContext"] = ctxMap
		}
	}
	return msg
}

func appendTools(msg map[string]any, item ir.UnifiedItem) []any {
	assistant, _ := msg["assistantResponseMessage"].(map[string]any)
	uses, _ := assistant["toolUses"].([]any)
	var input any
	if strings.TrimSpace(item.Arguments) != "" {
		if err := json.Unmarshal([]byte(item.Arguments), &input); err != nil {
			input = map[string]any{}
		}
	} else {
		input = map[string]any{}
	}
	return append(uses, map[string]any{
		"toolUseId": item.CallID,
		"name":      item.Name,
		"input":     input,
	})
}

func toKiroTools(tools []ir.UnifiedToolSpec) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		spec := map[string]any{"name": t.Name}
		if t.Description != "" {
			spec["description"] = t.Description
		}
		schema := map[string]any{"type": "object", "properties": map[string]any{}}
		if len(t.Parameters) > 0 {
			var raw any
			if err := json.Unmarshal(t.Parameters, &raw); err == nil {
				if obj, ok := raw.(map[string]any); ok {
					schema = obj
				}
			}
		}
		spec["inputSchema"] = map[string]any{"json": schema}
		out = append(out, map[string]any{"toolSpecification": spec})
	}
	return out
}

func stableConversationID(req *ir.UnifiedChatRequest) string {
	seed := req.LastUserText()
	if seed == "" {
		seed = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	sum := sha256.Sum256([]byte(seed))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

var regionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-\d$`)

func regionFromProfileArn(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) > 3 && regionPattern.MatchString(parts[3]) {
		return parts[3]
	}
	return ""
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func mapHTTPError(status int, body string, hdr http.Header) *ir.APIError {
	msg := truncate(body, 300)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Kiro 鉴权失败（%d）：%s", status, msg).WithProvider("kiro")
	case status == http.StatusTooManyRequests:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "Kiro 限流：%s", msg).
			WithProvider("kiro").WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, msg))
	case status == http.StatusPaymentRequired:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Kiro 账户余额不足（402）：%s", msg).
			WithProvider("kiro").WithRetryable(true).WithStatus(http.StatusPaymentRequired)
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "Kiro 拒绝请求：%s", msg).WithProvider("kiro")
	case status >= 500:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Kiro 上游 %d：%s", status, msg).
			WithProvider("kiro").WithRetryable(true).WithStatus(http.StatusBadGateway)
	default:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Kiro 上游返回 %d：%s", status, msg).WithProvider("kiro")
	}
}

func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// --- AWS EventStream 解析 ---

func parseEventStream(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
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
	currentTool := ""

	dec := wirecodec.NewAWSEventStreamDecoder(r)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		msg, err := dec.Next()
		if err != nil {
			break
		}
		key := firstNonEmpty(msg.Headers["exception-type"], msg.Headers["event-type"])
		if key == "" {
			continue
		}
		var payload map[string]any
		_ = json.Unmarshal(msg.Payload, &payload)

		switch key {
		case "assistantResponseEvent":
			if content, _ := payload["content"].(string); content != "" {
				if !emit(ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: content}) {
					return
				}
			}
		case "reasoningContentEvent":
			if text, _ := payload["text"].(string); text != "" {
				if !emit(ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: text}) {
					return
				}
			}
			if sig, _ := payload["signature"].(string); sig != "" {
				if !emit(ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, ThoughtSignature: sig}) {
					return
				}
			}
		case "toolUseEvent":
			name, _ := payload["name"].(string)
			id, _ := payload["toolUseId"].(string)
			input := toolInput(payload["input"])
			if name != "" && id != "" {
				sawTool = true
				currentTool = id
				if !emit(ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: id, ToolName: name}) {
					return
				}
			}
			if input != "" {
				if currentTool == "" {
					currentTool = id
				}
				if !emit(ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ToolCallID: currentTool, ArgsDelta: input}) {
					return
				}
			}
		case "metadataEvent":
			if usage, ok := payload["tokenUsage"].(map[string]any); ok {
				emit(ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: &ir.Usage{
					InputTokens:     lookupInt(usage, "uncachedInputTokens"),
					OutputTokens:    lookupInt(usage, "outputTokens"),
					TotalTokens:     lookupInt(usage, "totalTokens"),
					CachedTokens:    lookupInt(usage, "cacheReadInputTokens"),
					ReasoningTokens: 0,
				}})
			}
			if stop, _ := payload["stopReason"].(string); stop != "" {
				finish = mapStopReason(stop)
			}
		case "contextUsageEvent", "meteringEvent", "codeReferenceEvent", "documentCitationEvent", "toolResultEvent":
			// 暂无对应 IR 事件，忽略。
		case "error", "throttlingError", "validationError", "serviceUnavailableError",
			"ThrottlingException", "ValidationException", "ServiceUnavailableException",
			"InternalServerException":
			emit(ir.UnifiedStreamEvent{Type: ir.EventError, Err: mapKiroStreamError(key, payload)})
			return
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

func mapKiroStreamError(key string, payload map[string]any) *ir.APIError {
	msg, _ := payload["message"].(string)
	if msg == "" {
		msg, _ = payload["error"].(string)
	}
	if msg == "" {
		msg = key
	}
	switch key {
	case "validationError", "ValidationException":
		return ir.NewAPIError(ir.ErrCodeBadRequest, "Kiro 请求校验失败："+msg).WithProvider("kiro")
	case "throttlingError", "ThrottlingException":
		// 流内限流事件拿不到 HTTP 响应头（外层已是 200 的流），
		// 只能从错误文案里找 "resets in 1h2m3s" 这类提示。
		return ir.NewAPIError(ir.ErrCodeUpstreamRateLimit, "Kiro 限流："+msg).
			WithProvider("kiro").WithRetryable(true).
			WithRetryAfter(provider.RetryAfterHint(nil, msg))
	case "serviceUnavailableError", "ServiceUnavailableException":
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Kiro 服务不可用："+msg).
			WithProvider("kiro").WithRetryable(true)
	default:
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Kiro 流内错误："+msg).WithProvider("kiro")
	}
}

func mapStopReason(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "END_TURN", "STOP_SEQUENCE", "COMPLETE":
		return "stop"
	case "MAX_TOKENS", "MAX_TOKEN":
		return "length"
	case "TOOL_USE", "TOOL_CALLS":
		return "tool_calls"
	case "CONTENT_FILTERED", "GUARDRAIL":
		return "content_filter"
	default:
		return "stop"
	}
}

func toolInput(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case map[string]any:
		if len(v) == 0 {
			return ""
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	return ""
}

func lookupInt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}
