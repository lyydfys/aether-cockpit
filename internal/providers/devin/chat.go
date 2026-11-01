package devin

// Devin CLI 对话渠道：把 IR 请求编码成 Connect-RPC GetChatMessage，
// 再把返回的 protobuf 帧翻译回 IR 事件。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// ChatStream 调用 Devin CLI 上游并流式回传。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	token := formatDevinSessionToken(firstNonEmpty(
		env.GetCredential("session_token"),
		env.GetCredential("api_key"),
		env.GetCredential("token"),
	))
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"Devin 账号缺少 session_token；请粘贴 Devin CLI 登录后的 devin-session-token$...").
			WithProvider(channelID)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = devinDefaultBaseURL
	}
	deviceSeed := strings.TrimSpace(env.GetCredential("device_seed"))

	rawModel := strings.TrimSpace(req.UpstreamModel)
	if rawModel == "" {
		rawModel = strings.TrimPrefix(req.Model, "devin/")
	}
	modelUID := resolveDevinChatModelUID(rawModel, req.ReasoningEffort, 0)

	prompts, err := buildDevinPrompts(req)
	if err != nil {
		return nil, err
	}

	maxTokens := 0
	if req.MaxOutputTokens != nil {
		maxTokens = *req.MaxOutputTokens
	}
	sessionID := devinSessionUUID(devinConversationSeed(env.AccountID, req))

	protoBytes := buildDevinGetChatMessageRequest(
		token, deviceSeed, modelUID, strings.TrimSpace(req.Instructions),
		prompts, buildDevinTools(req.Tools), req.Temperature, maxTokens,
		sessionID, sessionID,
	)
	framed := WrapConnectEnvelope(protoBytes)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+devinChatPath, bytes.NewReader(framed))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 Devin 请求失败").WithCause(err)
	}
	httpReq.Header.Set("Authorization", "Basic "+token+"-"+token)
	httpReq.Header.Set("Content-Type", "application/connect+proto")
	httpReq.Header.Set("Connect-Protocol-Version", "1")
	httpReq.Header.Set("Accept", "*/*")
	httpReq.Header.Set("Sentry-Trace", GenerateDevinSentryTrace())
	// 原生 devin-cli 不发 User-Agent；置空串抑制 Go 默认值。
	httpReq.Header["User-Agent"] = []string{""}

	resp, err := env.Client().Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "Devin 上游连接失败").
			WithCause(err).WithProvider(channelID).WithRetryable(true)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, devinHTTPError(resp.StatusCode, string(raw), resp.Header)
	}

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		p.streamDevinFrames(ctx, out, resp.Body)
	}()
	return out, nil
}

// streamDevinFrames 读 Connect 帧并翻译成 IR 事件。
func (p *Provider) streamDevinFrames(ctx context.Context, out chan<- ir.UnifiedStreamEvent, body io.Reader) {
	if !emitDevinEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventStart}) {
		return
	}
	thinkingBuf := &UTF8SplitBuffer{}
	contentBuf := &UTF8SplitBuffer{}

	type toolSlot struct {
		id       string
		name     string
		argsSeen bool
	}
	var (
		toolSeq                                                    int
		sawTool                                                    bool
		lastSlot                                                   *toolSlot
		byID                                                       = map[string]*toolSlot{}
		lastStop                                                   uint64
		usagePrompt, usageCompletion, usageCached, usageCacheWrite int64
		usageModel                                                 string
		dimGroups                                                  [][]byte
	)

	for {
		flag, payload, errRead := ReadConnectFrame(body)
		if errRead != nil {
			if errors.Is(errRead, io.EOF) || ctx.Err() != nil {
				break
			}
			emitDevinEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventError, Err: ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Devin 流读取失败：%v", errRead).WithProvider(channelID)})
			return
		}
		if flag&ConnectFlagEndStream != 0 {
			if code, errTrailer := ParseDevinTrailerError(payload); errTrailer != nil {
				emitDevinEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventError, Err: devinTrailerError(code, errTrailer)})
				return
			}
			break
		}
		frame, errParse := ParseDevinFrame(payload)
		if errParse != nil {
			continue
		}
		if frame.StopReason != 0 {
			lastStop = frame.StopReason
		}
		if frame.Usage != nil {
			if frame.Usage.PromptTokens > 0 {
				usagePrompt = frame.Usage.PromptTokens
			}
			if frame.Usage.CompletionTokens > 0 {
				usageCompletion = frame.Usage.CompletionTokens
			}
			if frame.Usage.CachedTokens > 0 {
				usageCached = frame.Usage.CachedTokens
			}
			if frame.Usage.CacheWriteTokens > 0 {
				usageCacheWrite = frame.Usage.CacheWriteTokens
			}
			if frame.Usage.ModelName != "" {
				usageModel = frame.Usage.ModelName
			}
		}
		dimGroups = append(dimGroups, frame.ResponseDimensionGroups...)

		if thinking := thinkingBuf.Feed([]byte(frame.ThinkingText)); thinking != "" {
			if !emitDevinEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: thinking}) {
				return
			}
		}
		if text := contentBuf.Feed([]byte(frame.ContentText)); text != "" {
			if !emitDevinEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: text}) {
				return
			}
		}

		for _, tc := range frame.ToolCallDeltas {
			slot := byID[tc.ID]
			if tc.ID == "" || slot == nil {
				if tc.ID == "" && lastSlot != nil && lastSlot.name != "" {
					slot = lastSlot
				}
			}
			if slot == nil {
				toolSeq++
				sawTool = true
				id := tc.ID
				if id == "" {
					id = "devin_call_" + itoa(toolSeq)
				}
				slot = &toolSlot{id: id, name: tc.Name}
				byID[id] = slot
				lastSlot = slot
				if !emitDevinEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: id, ToolName: tc.Name}) {
					return
				}
			} else {
				if slot.name == "" && tc.Name != "" {
					slot.name = tc.Name
					if !emitDevinEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallStart, ToolCallID: slot.id, ToolName: tc.Name}) {
						return
					}
				}
				if tc.ID != "" && slot.id != tc.ID {
					byID[tc.ID] = slot
					slot.id = tc.ID
				}
				lastSlot = slot
			}
			args := tc.Arguments
			if args == "" {
				args = tc.InvalidJSONStr
			}
			if args != "" {
				slot.argsSeen = true
				if !emitDevinEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventToolCallDelta, ToolCallID: slot.id, ArgsDelta: args}) {
					return
				}
			}
		}
	}

	if pt, ct, cached, found := ParseDevinResponseDimensionGroups(dimGroups...); found {
		if usagePrompt == 0 {
			usagePrompt = pt
		}
		if usageCompletion == 0 {
			usageCompletion = ct
		}
		if usageCached == 0 {
			usageCached = cached
		}
	}
	if usagePrompt > 0 || usageCompletion > 0 {
		emitDevinEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: &ir.Usage{
			InputTokens:  int(usagePrompt),
			OutputTokens: int(usageCompletion),
			TotalTokens:  int(usagePrompt + usageCompletion),
			CachedTokens: int(usageCached),
		}})
	}
	_ = usageModel
	_ = usageCacheWrite

	finish := "stop"
	switch {
	case sawTool || lastStop == 10:
		finish = "tool_calls"
	case lastStop == 3:
		finish = "length"
	}
	emitDevinEvent(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
}

// buildDevinPrompts 把 IR 历史折叠成 DevinPrompt 列表。
func buildDevinPrompts(req *ir.UnifiedChatRequest) ([]devinPrompt, error) {
	var out []devinPrompt
	for _, it := range req.Items {
		switch it.Type {
		case ir.ItemTypeMessage:
			if it.IsBlank() {
				continue
			}
			source := 2
			if it.Role == "user" {
				source = 1
			}
			out = append(out, devinPrompt{Source: source, Content: it.Text, Images: devinImagesOf(it)})
		case ir.ItemTypeFunctionCall:
			call := devinToolCall{ID: it.CallID, Name: it.Name, Arguments: it.Arguments}
			if n := len(out); n > 0 && out[n-1].Source == 2 {
				out[n-1].ToolCalls = append(out[n-1].ToolCalls, call)
			} else {
				out = append(out, devinPrompt{Source: 2, ToolCalls: []devinToolCall{call}})
			}
		case ir.ItemTypeFunctionCallOutput:
			out = append(out, devinPrompt{Source: 4, ToolCallID: it.CallID, Content: it.Output})
		}
	}
	for _, p := range out {
		if p.Source == 1 && strings.TrimSpace(p.Content) != "" {
			return out, nil
		}
	}
	return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "Devin 需要至少一条有内容的用户消息").WithProvider(channelID)
}

func devinImagesOf(it ir.UnifiedItem) []devinImage {
	var images []devinImage
	for _, att := range it.Attachments {
		if att.Kind != "" && att.Kind != "image" {
			continue
		}
		data := strings.TrimSpace(att.DataB64)
		if data == "" {
			continue
		}
		images = append(images, devinImage{Base64Data: data, MimeType: att.MIME})
	}
	return images
}

func buildDevinTools(specs []ir.UnifiedToolSpec) []devinTool {
	out := make([]devinTool, 0, len(specs))
	for _, t := range specs {
		if strings.TrimSpace(t.Name) == "" {
			continue
		}
		out = append(out, devinTool{Name: t.Name, Description: t.Description, Parameters: []byte(t.Parameters)})
	}
	return out
}

// devinConversationSeed 用账号 + 首条用户消息派生稳定会话 id。
func devinConversationSeed(accountID string, req *ir.UnifiedChatRequest) string {
	first := ""
	for _, it := range req.Items {
		if it.Type == ir.ItemTypeMessage && it.Role == "user" && strings.TrimSpace(it.Text) != "" {
			first = it.Text
			break
		}
	}
	sum := sha256.Sum256([]byte(accountID + "\x00" + strings.TrimSpace(req.Instructions) + "\x00" + first))
	return hex.EncodeToString(sum[:16])
}

// formatDevinSessionToken 补齐 Devin CLI 的强制前缀。
func formatDevinSessionToken(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return ""
	}
	const prefix = "devin-session-token$"
	if strings.HasPrefix(t, prefix) {
		return t
	}
	if strings.HasPrefix(t, "eyJ") {
		return prefix + t
	}
	return t
}

func devinHTTPError(status int, body string, hdr http.Header) *ir.APIError {
	msg := truncateDevin(body, 400)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Devin 鉴权失败（%d）：%s", status, msg).WithProvider(channelID)
	case status == http.StatusTooManyRequests:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "Devin 限流（%d）：%s", status, msg).
			WithProvider(channelID).WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, msg))
	case status >= 500:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Devin 上游 %d：%s", status, msg).
			WithProvider(channelID).WithRetryable(true).WithStatus(http.StatusBadGateway)
	default:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Devin 上游返回 %d：%s", status, msg).WithProvider(channelID)
	}
}

func devinTrailerError(status int, cause error) *ir.APIError {
	msg := cause.Error()
	switch {
	case status == http.StatusUnauthorized:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "Devin 鉴权失败：%s", msg).WithProvider(channelID)
	case status == http.StatusTooManyRequests:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "Devin 额度/限流：%s", msg).
			WithProvider(channelID).WithRetryable(true).WithStatus(http.StatusTooManyRequests)
	case status == http.StatusBadRequest:
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "Devin 拒绝请求：%s", msg).WithProvider(channelID)
	default:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "Devin 上游错误：%s", msg).
			WithProvider(channelID).WithRetryable(status >= 500)
	}
}

func emitDevinEvent(ctx context.Context, out chan<- ir.UnifiedStreamEvent, ev ir.UnifiedStreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncateDevin(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
