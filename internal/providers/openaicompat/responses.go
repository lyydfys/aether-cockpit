package openaicompat

// OpenAI Responses 上游协议（API-key 渠道可选 api_protocol=responses）。
// 只做 IR ↔ Responses 的转换；鉴权/代理/签名复用开放渠道的账号环境。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
)

func (p *Provider) chatResponses(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest, sp *spec.Spec) (<-chan ir.UnifiedStreamEvent, error) {
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(p.cfg.BaseURL), "/")
	}
	if base == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Responses 协议需要账号 Base URL").WithProvider(p.cfg.ID)
	}
	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = stripProviderPrefix(req.Model, p.cfg.ID)
	}
	if sp != nil {
		model = sp.MapModel(model)
	}
	if model == "" {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Responses 缺少模型名").WithProvider(p.cfg.ID)
	}
	payload := buildResponsesPayload(req, model)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Responses 请求序列化失败").WithCause(err)
	}
	endpoint := base + "/responses"
	if sp != nil {
		endpoint = base + sp.Endpoint("responses", "/responses")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Responses 请求失败").WithCause(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	apiKey := strings.TrimSpace(env.GetCredential("api_key"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(env.GetCredential("access_token"))
	}
	awsAccess := strings.TrimSpace(env.GetCredential("aws_access_key_id"))
	awsSecret := strings.TrimSpace(env.GetCredential("aws_secret_access_key"))
	if p.cfg.SigV4Service != "" && awsAccess != "" && awsSecret != "" {
		region := strings.TrimSpace(env.GetCredential("region"))
		if region == "" {
			region = bedrockRegionFromHost(httpReq.URL.Host)
		}
		if err := signAWSV4(httpReq, body, awsAccess, awsSecret,
			env.GetCredential("aws_session_token"), region, p.cfg.SigV4Service, time.Now()); err != nil {
			return nil, ir.NewAPIError(ir.ErrCodeInternal, "AWS SigV4 签名失败: "+err.Error()).WithProvider(p.cfg.ID)
		}
	} else if apiKey != "" {
		if p.cfg.AuthStyle == "api-key" {
			httpReq.Header.Set("api-key", apiKey)
		} else {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}
	} else if !p.cfg.NoKey {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "Responses 账号缺少 api_key").WithProvider(p.cfg.ID)
	}
	if sp != nil {
		if sp.ClientVersion != "" {
			httpReq.Header.Set("User-Agent", "aether-cockpit/"+sp.ClientVersion)
		}
		if len(sp.HeadersTemplate) > 0 {
			for k, v := range sp.HeadersTemplate {
				if httpReq.Header.Get(k) == "" {
					httpReq.Header.Set(k, v)
				}
			}
		}
	}
	resp, err := env.Client().Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Responses 上游连接失败").WithCause(err).
			WithProvider(p.cfg.ID).WithRetryable(true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, mapStatusCode(resp.StatusCode, strings.TrimSpace(string(raw)), resp.Header).WithProvider(p.cfg.ID)
	}
	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "event-stream") {
			parseResponsesJSON(ctx, out, resp.Body, p.cfg.ID)
			return
		}
		parseResponsesSSE(ctx, out, resp.Body, p.cfg.ID)
	}()
	return out, nil
}

func buildResponsesPayload(req *ir.UnifiedChatRequest, model string) map[string]any {
	payload := map[string]any{"model": model, "stream": true, "input": buildResponsesInput(req)}
	if s := responsesInstructions(req); s != "" {
		payload["instructions"] = s
	}
	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			item := map[string]any{"type": "function", "name": t.Name}
			if t.Description != "" {
				item["description"] = t.Description
			}
			if len(t.Parameters) > 0 {
				var schema any
				if json.Unmarshal(t.Parameters, &schema) == nil {
					item["parameters"] = schema
				}
			} else {
				item["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			tools = append(tools, item)
		}
		payload["tools"] = tools
		if req.ParallelToolCalls != nil {
			payload["parallel_tool_calls"] = *req.ParallelToolCalls
		}
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		payload["top_p"] = *req.TopP
	}
	// Responses 协议同样支持重复惩罚，一并透传。
	if req.FrequencyPenalty != nil {
		payload["frequency_penalty"] = *req.FrequencyPenalty
	}
	if req.PresencePenalty != nil {
		payload["presence_penalty"] = *req.PresencePenalty
	}
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		payload["max_output_tokens"] = *req.MaxOutputTokens
	}
	if tc := responsesToolChoice(req.ToolChoice); tc != nil {
		payload["tool_choice"] = tc
	}
	if format := responsesTextFormat(req.ResponseFormat); format != nil {
		payload["text"] = map[string]any{"format": format}
	}
	if strings.TrimSpace(req.ReasoningEffort) != "" {
		payload["reasoning"] = map[string]any{"effort": strings.TrimSpace(req.ReasoningEffort)}
	}
	return payload
}

func responsesInstructions(req *ir.UnifiedChatRequest) string {
	parts := make([]string, 0, 2)
	if s := strings.TrimSpace(req.Instructions); s != "" {
		parts = append(parts, s)
	}
	for _, it := range req.Items {
		if it.Type != ir.ItemTypeMessage {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(it.Role))
		if role != "system" && role != "developer" {
			continue
		}
		if s := strings.TrimSpace(it.Text); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

func buildResponsesInput(req *ir.UnifiedChatRequest) []any {
	out := make([]any, 0, len(req.Items))
	for _, it := range req.Items {
		switch it.Type {
		case ir.ItemTypeMessage:
			role := strings.ToLower(strings.TrimSpace(it.Role))
			if role == "system" || role == "developer" {
				continue
			}
			if role == "" {
				role = "user"
			}
			partType := "input_text"
			if role == "assistant" {
				partType = "output_text"
			}
			parts := make([]any, 0, len(it.Attachments)+1)
			if strings.TrimSpace(it.Text) != "" || len(it.Attachments) == 0 {
				parts = append(parts, map[string]any{"type": partType, "text": it.Text})
			}
			for _, att := range it.Attachments {
				url := att.URL
				if url == "" && att.DataB64 != "" {
					url = "data:" + att.MIME + ";base64," + att.DataB64
				}
				if url != "" {
					parts = append(parts, map[string]any{"type": "input_image", "image_url": url})
				}
			}
			out = append(out, map[string]any{"role": role, "content": parts})
		case ir.ItemTypeFunctionCall:
			out = append(out, map[string]any{
				"type": "function_call", "call_id": it.CallID, "name": it.Name,
				"arguments": defaultArgs(it.Arguments),
			})
		case ir.ItemTypeFunctionCallOutput:
			out = append(out, map[string]any{
				"type": "function_call_output", "call_id": it.CallID, "output": it.Output,
			})
		}
	}
	return out
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func responsesUsageToIR(u *responsesUsage) *ir.Usage {
	if u == nil {
		return nil
	}
	cached := 0
	if u.InputTokensDetails != nil {
		cached = u.InputTokensDetails.CachedTokens
	}
	reasoning := 0
	if u.OutputTokensDetails != nil {
		reasoning = u.OutputTokensDetails.ReasoningTokens
	}
	input := u.InputTokens - cached
	if input < 0 {
		input = 0
	}
	usage := &ir.Usage{InputTokens: input, CachedTokens: cached, OutputTokens: u.OutputTokens, ReasoningTokens: reasoning}
	usage.NormalizeTotal()
	return usage
}

func parseResponsesSSE(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader, providerID string) {
	dec := wirecodec.NewSSEDecoder(r)
	callNames := map[string]string{}
	callStarted := map[string]bool{}
	argsSeen := map[string]bool{}
	itemToCall := map[string]string{}
	sawTool := false
	finish := "stop"
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
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			ItemID   string          `json:"item_id"`
			CallID   string          `json:"call_id"`
			Name     string          `json:"name"`
			Item     json.RawMessage `json:"item"`
			Response json.RawMessage `json:"response"`
			Usage    *responsesUsage `json:"usage"`
			Error    *struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		name := strings.TrimSpace(frame.Event)
		if name == "" {
			name = ev.Type
		}
		name = strings.TrimPrefix(name, "response.")
		switch name {
		case "output_item.added":
			var item struct {
				ID      string `json:"id"`
				Type    string `json:"type"`
				CallID  string `json:"call_id"`
				Name    string `json:"name"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if json.Unmarshal(ev.Item, &item) != nil {
				continue
			}
			if item.Type == "function_call" {
				id := firstNonEmpty(item.CallID, item.ID, ev.ItemID)
				callNames[id] = item.Name
				callStarted[id] = true
				if item.ID != "" {
					itemToCall[item.ID] = id
				}
				sawTool = true
				emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: id, ToolName: item.Name})
			}
		case "output_text.delta":
			if ev.Delta != "" {
				emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: ev.Delta})
			}
		case "reasoning_summary_text.delta", "reasoning_text.delta":
			if ev.Delta != "" {
				emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: ev.Delta})
			}
		case "function_call_arguments.delta":
			id := firstNonEmpty(itemToCall[ev.ItemID], ev.CallID, ev.ItemID)
			if id != "" && ev.Delta != "" {
				argsSeen[id] = true
				sawTool = true
				emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ToolCallID: id, ArgsDelta: ev.Delta})
			}
		case "function_call_arguments.done":
			id := firstNonEmpty(itemToCall[ev.ItemID], ev.CallID, ev.ItemID)
			if id != "" && ev.Delta != "" && !argsSeen[id] {
				argsSeen[id] = true
				sawTool = true
				emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ToolCallID: id, ArgsDelta: ev.Delta})
			}
		case "output_item.done":
			var item struct {
				ID        string `json:"id"`
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if json.Unmarshal(ev.Item, &item) == nil && item.Type == "function_call" {
				id := firstNonEmpty(itemToCall[item.ID], item.CallID, item.ID, ev.ItemID)
				if id != "" {
					if !callStarted[id] {
						callStarted[id] = true
						sawTool = true
						emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: id, ToolName: firstNonEmpty(item.Name, callNames[id])})
					}
					if item.Arguments != "" && !argsSeen[id] {
						argsSeen[id] = true
						emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ToolCallID: id, ArgsDelta: item.Arguments})
					}
				}
			}
		case "completed", "incomplete":
			var payload struct {
				Usage             *responsesUsage `json:"usage"`
				IncompleteDetails *struct {
					Reason string `json:"reason"`
				} `json:"incomplete_details"`
			}
			_ = json.Unmarshal(ev.Response, &payload)
			if payload.Usage != nil {
				emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: responsesUsageToIR(payload.Usage)})
			}
			if sawTool {
				finish = "tool_calls"
			} else if payload.IncompleteDetails != nil && payload.IncompleteDetails.Reason == "max_output_tokens" {
				finish = "length"
			}
			emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
			return
		case "failed", "error":
			msg := firstNonEmpty(ev.Message, func() string {
				if ev.Error != nil {
					return ev.Error.Message
				}
				return ""
			}())
			emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventError,
				Err: ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Responses 流内错误: "+msg).WithProvider(providerID)})
			return
		}
	}
	if sawTool {
		finish = "tool_calls"
	}
	emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
}

func parseResponsesJSON(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader, providerID string) {
	raw, err := io.ReadAll(io.LimitReader(r, 32<<20))
	if err != nil {
		emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventError, Err: ir.NewAPIError(ir.ErrCodeUpstreamFailed, "读取 Responses 响应失败").WithCause(err)})
		return
	}
	var resp struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Summary   []struct {
				Text string `json:"text"`
			} `json:"summary"`
		} `json:"output"`
		Usage *responsesUsage `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventError,
			Err: ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Responses 响应无法解析: "+truncateStr(string(raw), 200)).WithProvider(providerID)})
		return
	}
	if resp.Error != nil {
		emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventError,
			Err: ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Responses 错误: "+resp.Error.Message).WithProvider(providerID)})
		return
	}
	finish := "stop"
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" || c.Type == "text" {
					emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: c.Text})
				}
			}
		case "reasoning":
			for _, s := range item.Summary {
				emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: s.Text})
			}
		case "function_call":
			finish = "tool_calls"
			emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: item.CallID, ToolName: item.Name})
			if item.Arguments != "" {
				emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ToolCallID: item.CallID, ArgsDelta: item.Arguments})
			}
		}
	}
	if resp.Usage != nil {
		emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: responsesUsageToIR(resp.Usage)})
	}
	emitCompat(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
}

func emitCompat(ctx context.Context, out chan<- ir.UnifiedStreamEvent, ev ir.UnifiedStreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
