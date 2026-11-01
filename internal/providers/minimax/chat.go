package minimax

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const (
	defaultMaxTokens = 8192
	anthropicVersion = "2023-06-01"
	placeholderKey   = "sk-xxx"
)

// ChatStream 把 IR 翻译成 MiniMax Code 的 Anthropic Messages 请求并解析响应。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	token := strings.TrimSpace(accessToken(env))
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth, "MiniMax Code 账号缺少 access token").WithProvider(p.profile.id)
	}
	model := strings.TrimSpace(req.UpstreamModel)
	if model == "" {
		model = strings.TrimPrefix(strings.TrimSpace(req.Model), p.profile.id+"/")
	}
	if model == "" {
		model = "MiniMax-M3"
	}

	body, err := json.Marshal(buildBody(req, model))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "MiniMax 请求序列化失败").WithCause(err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL(env)+"/mavis/api/v1/llm/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 MiniMax 请求失败").WithCause(err)
	}
	p.applyHeaders(httpReq, env, token, req)

	resp, err := env.Client().Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "MiniMax 上游连接失败").
			WithCause(err).WithProvider(p.profile.id).WithRetryable(true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, mapUpstreamError(p.profile.id, resp.StatusCode, string(raw), resp.Header)
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

func (p *Provider) baseURL(env *provider.AccountEnv) string {
	if v := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/"); v != "" {
		return v
	}
	return p.profile.llmBase
}

func (p *Provider) applyHeaders(req *http.Request, env *provider.AccountEnv, token string, in *ir.UnifiedChatRequest) {
	session := strings.TrimSpace(in.SessionKey)
	if session == "" {
		session = strings.TrimSpace(in.RequestID)
	}
	if session == "" {
		session = "aether-session"
	}
	_, offset := time.Now().Zone()
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-api-key", placeholderKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("User-Agent", "MiniMaxAgent")
	req.Header.Set("X-Mavis-Agent-Id", "main")
	req.Header.Set("X-Mavis-Timezone-Offset", strconv.Itoa(offset))
	req.Header.Set("X-Mavis-Session-Id", session)
	req.Header.Set("Content-Type", "application/json")
	if in.Stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
}

type anthBlock = map[string]any

func buildBody(req *ir.UnifiedChatRequest, model string) map[string]any {
	maxTokens := defaultMaxTokens
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		maxTokens = *req.MaxOutputTokens
	}
	body := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   buildMessages(req),
		"stream":     req.Stream,
	}
	if s := strings.TrimSpace(req.Instructions); s != "" {
		body["system"] = s
	}
	if tools := buildTools(req); len(tools) > 0 {
		body["tools"] = tools
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if len(req.StopSequences) > 0 {
		body["stop_sequences"] = req.StopSequences
	}
	return body
}

type anthMessage struct {
	Role    string      `json:"role"`
	Content []anthBlock `json:"content"`
}

// buildMessages 把 IR items 折成 Anthropic messages（相邻同角色合并）。
func buildMessages(req *ir.UnifiedChatRequest) []anthMessage {
	msgs := make([]anthMessage, 0, len(req.Items))
	appendBlock := func(role string, block anthBlock) {
		if n := len(msgs); n > 0 && msgs[n-1].Role == role {
			msgs[n-1].Content = append(msgs[n-1].Content, block)
			return
		}
		msgs = append(msgs, anthMessage{Role: role, Content: []anthBlock{block}})
	}
	for _, it := range req.Items {
		switch it.Type {
		case ir.ItemTypeMessage:
			if strings.TrimSpace(it.Text) == "" {
				continue
			}
			role := it.Role
			if role != "assistant" {
				role = "user"
			}
			appendBlock(role, anthBlock{"type": "text", "text": it.Text})
		case ir.ItemTypeFunctionCall:
			args := json.RawMessage(it.Arguments)
			if len(args) == 0 || !json.Valid(args) {
				args = json.RawMessage(`{}`)
			}
			appendBlock("assistant", anthBlock{
				"type": "tool_use", "id": it.CallID, "name": it.Name, "input": args,
			})
		case ir.ItemTypeFunctionCallOutput:
			appendBlock("user", anthBlock{
				"type": "tool_result", "tool_use_id": it.CallID, "content": it.Output,
			})
		}
	}
	return msgs
}

func buildTools(req *ir.UnifiedChatRequest) []map[string]any {
	out := make([]map[string]any, 0, len(req.Tools))
	for _, t := range req.Tools {
		if strings.TrimSpace(t.Name) == "" {
			continue
		}
		schema := json.RawMessage(t.Parameters)
		if len(schema) == 0 || !json.Valid(schema) {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, map[string]any{
			"name": t.Name, "description": t.Description, "input_schema": schema,
		})
	}
	return out
}

func mapUpstreamError(providerID string, status int, body string, hdr http.Header) *ir.APIError {
	msg := strings.TrimSpace(body)
	if len(msg) > 400 {
		msg = msg[:400]
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ir.NewAPIError(ir.ErrCodeUpstreamAuth, "MiniMax 拒绝了这个账号的登录态: "+msg).WithProvider(providerID)
	case status == http.StatusTooManyRequests:
		return ir.NewAPIError(ir.ErrCodeUpstreamRateLimit, "MiniMax 限流: "+msg).WithProvider(providerID).WithRetryable(true).
			WithRetryAfter(provider.RetryAfterHint(hdr, msg))
	case status == http.StatusBadRequest:
		return ir.NewAPIError(ir.ErrCodeBadRequest, "MiniMax 请求被拒绝: "+msg).WithProvider(providerID)
	case status >= 500:
		return ir.NewAPIError(ir.ErrCodeUpstreamFailed, fmt.Sprintf("MiniMax 上游 %d: %s", status, msg)).WithProvider(providerID).WithRetryable(true)
	}
	return ir.NewAPIError(ir.ErrCodeUpstreamFailed, fmt.Sprintf("MiniMax 上游 %d: %s", status, msg)).WithProvider(providerID)
}

// parseSSE 解析 Anthropic 风格事件流。
func parseSSE(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	type toolRef struct {
		id   string
		name string
	}
	tools := map[int]toolRef{}
	var responseID string
	var finish string
	var usage ir.Usage
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message *struct {
				ID    string `json:"id"`
				Usage struct {
					InputTokens          int `json:"input_tokens"`
					CacheReadInputTokens int `json:"cache_read_input_tokens"`
					CacheCreationTokens  int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Index        int `json:"index"`
			ContentBlock *struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta *struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error *struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				responseID = ev.Message.ID
				usage.InputTokens = ev.Message.Usage.InputTokens
				usage.CachedTokens = ev.Message.Usage.CacheReadInputTokens
				usage.CacheCreationTokens = ev.Message.Usage.CacheCreationTokens
				send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventStart, ResponseID: responseID})
			}
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				tools[ev.Index] = toolRef{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
				send(ctx, out, ir.UnifiedStreamEvent{
					Type: ir.EventToolCallStart, ToolCallID: ev.ContentBlock.ID, ToolName: ev.ContentBlock.Name,
				})
			}
		case "content_block_delta":
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: ev.Delta.Text})
			case "thinking_delta":
				send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: ev.Delta.Thinking})
			case "input_json_delta":
				ref := tools[ev.Index]
				send(ctx, out, ir.UnifiedStreamEvent{
					Type: ir.EventToolCallDelta, ToolCallID: ref.id, ToolName: ref.name, ArgsDelta: ev.Delta.PartialJSON,
				})
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				finish = mapStop(ev.Delta.StopReason)
			}
			if ev.Usage != nil {
				usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			usage.NormalizeTotal()
			send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: &usage})
			send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, ResponseID: responseID, FinishReason: finish})
			return
		case "error":
			msg := "MiniMax 上游流内错误"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventError, Err: ir.NewAPIError(ir.ErrCodeUpstreamFailed, msg)})
			return
		}
	}
	if finish == "" {
		finish = "stop"
	}
	usage.NormalizeTotal()
	send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: &usage})
	send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, ResponseID: responseID, FinishReason: finish})
}

func parseJSON(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader) {
	var resp struct {
		ID      string `json:"id"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens          int `json:"input_tokens"`
			OutputTokens         int `json:"output_tokens"`
			CacheReadInputTokens int `json:"cache_read_input_tokens"`
			CacheCreationTokens  int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventError, Err: ir.NewAPIError(ir.ErrCodeUpstreamFailed, "MiniMax 响应解析失败: "+err.Error())})
		return
	}
	send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventStart, ResponseID: resp.ID})
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: b.Text})
		case "thinking":
			send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: b.Text})
		case "tool_use":
			send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: b.ID, ToolName: b.Name})
			if len(b.Input) > 0 && string(b.Input) != "{}" {
				send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ToolCallID: b.ID, ToolName: b.Name, ArgsDelta: string(b.Input)})
			}
		}
	}
	u := ir.Usage{
		InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens,
		CachedTokens: resp.Usage.CacheReadInputTokens, CacheCreationTokens: resp.Usage.CacheCreationTokens,
	}
	u.NormalizeTotal()
	send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: &u})
	send(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, ResponseID: resp.ID, FinishReason: mapStop(resp.StopReason)})
}

func mapStop(reason string) string {
	switch reason {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	case "":
		return "stop"
	}
	return "stop"
}

func send(ctx context.Context, out chan<- ir.UnifiedStreamEvent, ev ir.UnifiedStreamEvent) {
	select {
	case out <- ev:
	case <-ctx.Done():
	}
}
