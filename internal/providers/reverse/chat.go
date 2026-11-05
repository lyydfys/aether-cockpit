package reverse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/rtstats"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/core/transport/wirecodec"
	"aether-cockpit/internal/provider"
)

// ChatStream 按 spec 描述的上游协议发起流式对话。
//
// 流程固定为：
//
//	spec 取参数 -> 构造请求体 -> 打鉴权/指纹/签名 -> HTTP -> 分帧 -> 字段映射 -> IR 事件
//
// 这样渠道之间的差异几乎全部收敛到 spec，而不是复制一份 provider 实现。
func (p *Provider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	sp := spec.Load(p.profile.ID)

	baseURL := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if baseURL == "" && sp != nil {
		baseURL = strings.TrimRight(strings.TrimSpace(sp.BaseURL), "/")
	}
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(p.profile.DefaultBaseURL), "/")
	}
	if baseURL == "" {
		return nil, p.notConfigured("base_url")
	}

	endpointPath := sp.Endpoint("chat", p.profile.DefaultChatPath)
	if endpointPath == "" {
		return nil, p.notConfigured("endpoints.chat")
	}

	cfg := chatConfig(sp)

	upstreamModel := strings.TrimSpace(req.UpstreamModel)
	if upstreamModel == autoModelUpstream {
		upstreamModel = strings.TrimSpace(env.GetCredential("model"))
		if upstreamModel == "" {
			return nil, ir.NewAPIError(ir.ErrCodeUnsupported,
				"使用了 /auto 模型，但账号没有 model 凭证；请重新做本机导入，或直接写 "+p.profile.ID+"/<上游模型名>").
				WithProvider(p.profile.ID)
		}
	}
	if upstreamModel == "" {
		upstreamModel = stripProviderPrefix(req.Model, p.profile.ID)
	}
	if sp != nil {
		upstreamModel = sp.MapModel(upstreamModel)
	}

	payload := buildPayload(req, upstreamModel, cfg.Request)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "请求序列化失败").WithCause(err)
	}

	endpoint := baseURL + endpointPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造上游请求失败").WithCause(err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	if err := applyAuth(httpReq, env, cfg.Auth); err != nil {
		return nil, err.WithProvider(p.profile.ID)
	}
	// UA 不是装饰：OpenCode 这类"官方/自部署混合池"会把 UA 计入路由决策
	// （实测官方命中率 Claude UA 13/16、Codex 10/16、通用 8/16）。我们至少
	// 不能自称 Go-http-client——那是最像爬虫的 UA，最容易被打到自部署 cell。
	//
	// 优先级：账号凭证里的 user_agent（用户显式指定，原样发送）
	//       > spec 里的 client_version（对应客户端的真实版本号）
	//       > 产品名 + 版本兜底。
	applyUserAgent(httpReq, env, sp)
	if len(p.profile.Headers) > 0 {
		applyHeaders(httpReq, env, p.profile.Headers)
	}
	if sp != nil {
		applyHeaders(httpReq, env, sp.HeadersTemplate)
		if sp.SignatureRule != nil {
			sig, tsVal, err := sp.SignatureRule.Sign(spec.SignInput{
				Method:   http.MethodPost,
				Path:     endpoint,
				Body:     body,
				DeviceID: env.Fingerprint.DeviceID,
			})
			if err != nil {
				return nil, ir.NewAPIError(ir.ErrCodeInternal, "签名计算失败").WithCause(err)
			}
			if sig != "" && sp.SignatureRule.HeaderName != "" {
				httpReq.Header.Set(sp.SignatureRule.HeaderName, sig)
			}
			if tsVal != "" && sp.SignatureRule.TimestampHeader != "" {
				httpReq.Header.Set(sp.SignatureRule.TimestampHeader, tsVal)
			}
		}
	}

	// OpenCode Go 要求会话标识：缺了会被上游 400 MissingSessionID 拒绝。
	// req.SessionKey 由网关按会话指纹算好，跨轮稳定；spec 显式设了同名头则尊重 spec。
	if p.profile.SessionHeader != "" {
		if v := strings.TrimSpace(req.SessionKey); v != "" && httpReq.Header.Get(p.profile.SessionHeader) == "" {
			httpReq.Header.Set(p.profile.SessionHeader, v)
		}
	}

	client := env.Client()

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游连接失败").
			WithCause(err).WithProvider(p.profile.ID).WithRetryable(true)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := readErrorBody(resp)
		ae := mapStatusCode(resp.StatusCode, msg, resp.Header).WithProvider(p.profile.ID)
		return nil, ae
	}

	// 统计"后端族群"分布（官方/中转/未知），供界面展示官转率。
	// 放在 HTTP body 层统一嗅探：流式与非流式、SSE 与 JSON 都能覆盖，
	// 且不改变调用方读到的字节（见 rtstats.Sniff 的测试）。
	resp.Body = rtstats.Sniff(resp.Body, p.profile.ID)

	format := strings.ToLower(strings.TrimSpace(cfg.Response.Format))
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if format == "" {
		switch {
		case strings.Contains(ct, "ndjson"):
			format = "ndjson"
		case strings.Contains(ct, "event-stream"):
			format = "sse"
		case ct != "":
			format = "json"
		default:
			format = "sse"
		}
	}
	// 明确写了 sse 但上游实际直接返回整段 JSON 时，按 JSON 处理，
	// 否则会把一段完整响应当成空流。
	if format == "sse" && ct != "" && !strings.Contains(ct, "event-stream") {
		format = "json"
	}

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		parseResponse(ctx, out, resp.Body, format, cfg)
	}()

	return out, nil
}

// notConfigured 生成一条可执行的配置错误。
func (p *Provider) notConfigured(field string) *ir.APIError {
	return ir.NewAPIErrorf(ir.ErrCodeUnsupported,
		"渠道 %s 尚未配置协议参数（缺少 %s）。请按 docs/ADD_PROVIDER.md 抓包后填写 configs/specs/%s.json，再点「重载参数」。",
		p.profile.ID, field, p.profile.ID).WithProvider(p.profile.ID)
}

// applyAuth 把账号凭证写入请求头。
func applyAuth(r *http.Request, env *provider.AccountEnv, a AuthMapping) *ir.APIError {
	if a.None {
		return nil
	}

	key, value := pickCredential(env, a.Credential)
	if value == "" {
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth,
			"账号缺少凭证：请填写 %s", firstString(a.Credential, "access_token / token / api_key / cookie"))
	}

	if a.Cookie || key == "cookie" || strings.EqualFold(a.Header, "cookie") {
		r.Header.Set("Cookie", value)
		return nil
	}

	header := a.Header
	if header == "" {
		header = "Authorization"
	}
	if a.NoPrefix || strings.HasPrefix(value, "Bearer ") {
		r.Header.Set(header, value)
		return nil
	}
	prefix := a.Prefix
	if prefix == "" {
		prefix = "Bearer "
	}
	if !strings.HasSuffix(prefix, " ") {
		prefix += " "
	}
	r.Header.Set(header, prefix+value)
	return nil
}

// pickCredential 按优先级取一个凭证字段。
func pickCredential(env *provider.AccountEnv, preferred string) (key, value string) {
	if preferred != "" {
		return preferred, env.GetCredential(preferred)
	}
	for _, k := range []string{"access_token", "token", "api_key", "cookie"} {
		if v := env.GetCredential(k); v != "" {
			return k, v
		}
	}
	return "", ""
}

// applyHeaders 应用 spec 头模板，额外支持 {{.Credentials.xxx}} 占位符。
// applyUserAgent 设置上游 User-Agent。
//
// 为什么值得单独一个函数：同一份代码要覆盖三种来源，而且 UA 会实际影响
// "官方/自部署混合池"的路由决策（见调用处注释）。
//
//   - 账号凭证 user_agent：用户显式指定，原样发送，不做任何拼接；
//   - spec.client_version：对应客户端的真实版本，拼成 "<产品>/<版本>"；
//   - 都没有：产品名 + 版本兜底，绝不留 Go 默认的 Go-http-client。
func applyUserAgent(r *http.Request, env *provider.AccountEnv, sp *spec.Spec) {
	if env != nil {
		if ua := strings.TrimSpace(env.GetCredential("user_agent")); ua != "" {
			r.Header.Set("User-Agent", ua)
			return
		}
	}
	if sp != nil && sp.ClientVersion != "" {
		r.Header.Set("User-Agent", "aether-cockpit/"+sp.ClientVersion)
		return
	}
	r.Header.Set("User-Agent", "aether-cockpit/"+reverseClientVersion)
}

// reverseClientVersion 是没有 spec/client_version 时的兜底版本号。
// 只是让 UA 可读、可辨识；不参与任何协议语义。
const reverseClientVersion = "1.0"

func applyHeaders(r *http.Request, env *provider.AccountEnv, tmpl map[string]string) {
	for k, t := range tmpl {
		v := expandTemplate(t, env)
		if v == "" {
			continue
		}
		r.Header.Set(k, v)
	}
}

func expandTemplate(tmpl string, env *provider.AccountEnv) string {
	repl := map[string]string{
		"{{.Fingerprint.MachineID}}":     env.Fingerprint.MachineID,
		"{{.Fingerprint.DeviceID}}":      env.Fingerprint.DeviceID,
		"{{.Fingerprint.MAC}}":           env.Fingerprint.MAC,
		"{{.Fingerprint.ClientVersion}}": env.Fingerprint.ClientVersion,
		"{{.Fingerprint.Platform}}":      env.Fingerprint.Platform,
	}
	out := tmpl
	for k, v := range repl {
		out = strings.ReplaceAll(out, k, v)
	}
	for k, v := range env.Fingerprint.Extra {
		out = strings.ReplaceAll(out, "{{.Fingerprint.Extra."+k+"}}", v)
	}
	for k, v := range env.Credentials {
		out = strings.ReplaceAll(out, "{{.Credentials."+k+"}}", v)
	}
	return out
}

// buildPayload 把 IR 请求映射成上游请求体。
func buildPayload(req *ir.UnifiedChatRequest, upstreamModel string, rm RequestMapping) map[string]any {
	payload := make(map[string]any, 8+len(rm.ExtraFields))

	// 先放固定字段，再让 ExtraFields 覆盖，方便上层补偿上游要求的额外参数。
	for k, v := range rm.ExtraFields {
		payload[k] = v
	}

	payload[rm.ModelField] = upstreamModel
	if rm.MessagesField != "" {
		payload[rm.MessagesField] = toMessages(req, rm)
	}
	if rm.StreamField != "" {
		payload[rm.StreamField] = rm.StreamValue
	}
	if rm.SystemField != "" && strings.TrimSpace(req.Instructions) != "" && !*rm.SystemInMessages {
		payload[rm.SystemField] = req.Instructions
	}

	if len(req.Tools) > 0 && rm.ToolsField != "" {
		payload[rm.ToolsField] = toTools(req.Tools)
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

// toMessages 把 IR Item 列表转成传统 messages 数组。
func toMessages(req *ir.UnifiedChatRequest, rm RequestMapping) []map[string]any {
	msgs := make([]map[string]any, 0, len(req.Items)+1)

	if s := strings.TrimSpace(req.Instructions); s != "" && rm.SystemInMessages != nil && *rm.SystemInMessages {
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
			// assistant 带 tool_calls 时 content 必须是 null，空串会被部分上游拒绝。
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

	for _, it := range req.Items {
		switch it.Type {
		case ir.ItemTypeMessage:
			flushCalls()
			content := any(it.Text)
			if len(it.Attachments) > 0 {
				content = buildMultimodalContent(it)
			}
			msgs = append(msgs, map[string]any{"role": it.Role, "content": content})

		case ir.ItemTypeReasoning:
			pendingReasoning += it.Text

		case ir.ItemTypeFunctionCall:
			pendingCalls = append(pendingCalls, map[string]any{
				"id":   it.CallID,
				"type": "function",
				"function": map[string]any{
					"name":      it.Name,
					"arguments": defaultArgs(it.Arguments),
				},
			})

		case ir.ItemTypeFunctionCallOutput:
			flushCalls()
			msgs = append(msgs, map[string]any{
				"role":         "tool",
				"tool_call_id": it.CallID,
				"content":      it.Output,
			})
		}
	}
	flushCalls()
	return msgs
}

func buildMultimodalContent(it ir.UnifiedItem) []map[string]any {
	parts := make([]map[string]any, 0, len(it.Attachments)+1)
	if strings.TrimSpace(it.Text) != "" {
		parts = append(parts, map[string]any{"type": "text", "text": it.Text})
	}
	for _, att := range it.Attachments {
		url := att.URL
		if url == "" && att.DataB64 != "" {
			url = "data:" + att.MIME + ";base64," + att.DataB64
		}
		if url == "" {
			continue
		}
		parts = append(parts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": url},
		})
	}
	return parts
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
		} else {
			fn["parameters"] = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

func defaultArgs(s string) string {
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

func readErrorBody(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	resp.Body.Close()
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

func mapStatusCode(code int, body string, hdr http.Header) *ir.APIError {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamAuth, "上游鉴权失败: %s", body)
	case code == http.StatusTooManyRequests:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit, "上游限流: %s", body).
			WithRetryable(true).WithStatus(http.StatusTooManyRequests).
			WithRetryAfter(provider.RetryAfterHint(hdr, body))
	case code == http.StatusPaymentRequired:
		// 402 = 上游账户没钱（OpenCode "Insufficient account funds" 一类）。
		// 可换账号重试，但同一账号要进耗尽态，别当作普通 5xx 反复撞。
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "上游账户余额不足（402）：%s", body).
			WithRetryable(true).WithStatus(http.StatusPaymentRequired)
	case code == http.StatusBadRequest || code == http.StatusUnprocessableEntity:
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "上游拒绝请求: %s", body)
	case code >= 500:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "上游 %d: %s", code, body).
			WithRetryable(true).WithStatus(http.StatusBadGateway)
	default:
		return ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "上游返回 %d: %s", code, body)
	}
}

// --- 流解析 ---

func parseResponse(ctx context.Context, out chan<- ir.UnifiedStreamEvent, r io.Reader, format string, cfg ChatConfig) {
	p := &streamParser{
		ctx:           ctx,
		out:           out,
		cfg:           cfg,
		toolIndexToID: make(map[int]string, 4),
		toolStarted:   make(map[int]bool, 4),
	}

	switch format {
	case "json":
		p.parseWholeJSON(r)
	case "ndjson":
		p.parseFrames(wirecodec.NewNDJSONDecoder(r))
	case "aws_eventstream":
		p.parseAWS(r)
	default:
		p.parseFrames(wirecodec.NewSSEDecoder(r))
	}
	p.finishStream()
}

type frameDecoder interface {
	Next() (wirecodec.Frame, error)
}

type streamParser struct {
	ctx      context.Context
	out      chan<- ir.UnifiedStreamEvent
	cfg      ChatConfig
	started  bool
	finished bool
	finish   string
	sawTool  bool

	toolIndexToID map[int]string
	toolStarted   map[int]bool
}

func (p *streamParser) emit(ev ir.UnifiedStreamEvent) bool {
	select {
	case p.out <- ev:
		return true
	case <-p.ctx.Done():
		return false
	}
}

func (p *streamParser) ensureStart(responseID string) {
	if p.started {
		return
	}
	p.started = true
	p.emit(ir.UnifiedStreamEvent{Type: ir.EventStart, ResponseID: responseID})
}

func (p *streamParser) parseFrames(dec frameDecoder) {
	// 先发一个空的 Start，保证下游总能收到完整生命周期；
	// 如果首帧带了上游会话 ID，再补一个带 ID 的 Start（transducer 会忽略重复 Start）。
	p.ensureStart("")

	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		frame, err := dec.Next()
		if err != nil {
			return
		}
		if p.handleFrame(frame.Data) {
			return
		}
	}
}

func (p *streamParser) parseAWS(r io.Reader) {
	p.ensureStart("")
	dec := wirecodec.NewAWSEventStreamDecoder(r)
	for {
		select {
		case <-p.ctx.Done():
			return
		default:
		}
		msg, err := dec.Next()
		if err != nil {
			return
		}
		if msg.MessageType() == "exception" || msg.MessageType() == "error" {
			detail := msg.ExceptionType()
			if detail == "" {
				detail = string(msg.Payload)
			}
			p.emit(ir.UnifiedStreamEvent{
				Type: ir.EventError,
				Err:  ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游流内错误: "+detail),
			})
			return
		}
		if p.handleFrame(msg.Payload) {
			return
		}
	}
}

func (p *streamParser) parseWholeJSON(r io.Reader) {
	p.ensureStart("")
	body, err := io.ReadAll(io.LimitReader(r, 32<<20))
	if err != nil {
		p.emit(ir.UnifiedStreamEvent{
			Type: ir.EventError,
			Err:  ir.NewAPIError(ir.ErrCodeUpstreamFailed, "读取上游响应失败").WithCause(err),
		})
		return
	}

	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		p.emit(ir.UnifiedStreamEvent{
			Type: ir.EventError,
			Err:  ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "上游响应无法解析: %s", truncate(string(body), 200)),
		})
		return
	}
	p.handleWhole(root)
}

// handleFrame 处理一条分帧载荷，返回 true 表示流应结束。
func (p *streamParser) handleFrame(data []byte) bool {
	s := strings.TrimSpace(string(data))
	if s == "" {
		return false
	}
	for _, marker := range p.cfg.Response.DoneMarkers {
		if marker != "" && s == marker {
			return true
		}
	}

	var root any
	if err := json.Unmarshal([]byte(s), &root); err != nil {
		// 不是 JSON 的帧（纯文本增量）直接当正文，避免整条流被丢掉。
		return !p.emit(ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: s})
	}
	return p.handleRoot(root, false)
}

// handleWhole 处理一次性 JSON 响应。
func (p *streamParser) handleWhole(root any) {
	p.handleRoot(root, true)
}

func (p *streamParser) handleRoot(root any, whole bool) bool {
	if root == nil {
		return false
	}
	if s, ok := root.(string); ok {
		return !p.emit(ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: s})
	}

	if p.cfg.Response.SessionIDPath != "" {
		if id := lookupString(root, p.cfg.Response.SessionIDPath); id != "" {
			if p.started {
				p.emit(ir.UnifiedStreamEvent{Type: ir.EventStart, ResponseID: id})
			} else {
				p.ensureStart(id)
			}
		}
	}

	if p.cfg.Response.ErrorPath != "" {
		if v := lookup(root, p.cfg.Response.ErrorPath); v != nil && !isEmptyValue(v) {
			msg := p.cfg.Response.ErrorMessagePath
			detail := ""
			if msg != "" {
				detail = lookupString(root, msg)
			}
			if detail == "" {
				detail = stringify(v)
			}
			p.emit(ir.UnifiedStreamEvent{
				Type: ir.EventError,
				Err:  ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游流内错误: "+detail),
			})
			return true
		}
	}

	if s := lookupString(root, p.cfg.Response.TextPath); s != "" {
		if !p.emit(ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: s}) {
			return true
		}
	} else if whole {
		// 一次性 JSON 的正文往往在 message 而不是 delta 里。
		if s := firstString(
			lookupString(root, "choices.0.message.content"),
			lookupString(root, "message.content"),
			lookupString(root, "content"),
			lookupString(root, "text"),
		); s != "" {
			if !p.emit(ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: s}) {
				return true
			}
		}
	}

	for _, path := range p.cfg.Response.ReasoningPaths {
		if s := lookupString(root, path); s != "" {
			if !p.emit(ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: s}) {
				return true
			}
		}
	}

	toolPath := p.cfg.Response.ToolCallsPath
	if whole {
		if s := lookupSlice(root, toolPath); len(s) == 0 {
			if s := lookupSlice(root, "choices.0.message.tool_calls"); len(s) > 0 {
				toolPath = "choices.0.message.tool_calls"
			} else if s := lookupSlice(root, "tool_calls"); len(s) > 0 {
				toolPath = "tool_calls"
			}
		}
	}
	if calls := lookupSlice(root, toolPath); len(calls) > 0 {
		for i, raw := range calls {
			obj, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			idx := i
			if v := lookup(obj, p.cfg.Response.ToolIndexPath); v != nil {
				idx = lookupInt(obj, p.cfg.Response.ToolIndexPath)
			}
			id := lookupString(obj, p.cfg.Response.ToolIDPath)
			if id == "" {
				id = p.toolIndexToID[idx]
			} else {
				p.toolIndexToID[idx] = id
			}
			name := lookupString(obj, p.cfg.Response.ToolNamePath)
			args := lookupString(obj, p.cfg.Response.ToolArgsPath)
			if name != "" && !p.toolStarted[idx] {
				p.toolStarted[idx] = true
				p.sawTool = true
				if !p.emit(ir.UnifiedStreamEvent{
					Type:       ir.EventToolCallStart,
					ToolCallID: id,
					ToolName:   name,
				}) {
					return true
				}
			}
			if args != "" {
				if !p.toolStarted[idx] {
					p.toolStarted[idx] = true
					p.sawTool = true
					if !p.emit(ir.UnifiedStreamEvent{
						Type:       ir.EventToolCallStart,
						ToolCallID: id,
						ToolName:   "",
					}) {
						return true
					}
				}
				if !p.emit(ir.UnifiedStreamEvent{
					Type:       ir.EventToolCallDelta,
					ToolCallID: id,
					ArgsDelta:  args,
				}) {
					return true
				}
			}
		}
	}

	if p.cfg.Response.FinishPath != "" {
		if v := lookupString(root, p.cfg.Response.FinishPath); v != "" {
			p.finish = v
		}
	}

	if p.cfg.Response.UsagePath != "" {
		if u := lookupMap(root, p.cfg.Response.UsagePath); u != nil {
			cached := lookupInt(u, p.cfg.Response.UsageCachedPath)
			// spec 指向的上游基本是 OpenAI 兼容形态：input 路径（prompt_tokens）
			// 含 cached。不拆开的话，一旦 spec 配了 usage_cached_path，
			// 命中量就会被算两遍（总量翻倍、命中率恒 < 50%）。
			usage := &ir.Usage{
				InputTokens:     ir.SplitPromptCache(lookupInt(u, p.cfg.Response.UsageInputPath), cached),
				OutputTokens:    lookupInt(u, p.cfg.Response.UsageOutputPath),
				CachedTokens:    cached,
				ReasoningTokens: lookupInt(u, p.cfg.Response.UsageReasoningPath),
			}
			usage.NormalizeTotal()
			// 上游给了更大的 total（含我们没建模的类别）时以它为准，
			// addTokens 会把差额补回输入，避免总量丢失。
			if upTotal := lookupInt(u, p.cfg.Response.UsageTotalPath); upTotal > usage.TotalTokens {
				usage.TotalTokens = upTotal
			}
			p.emit(ir.UnifiedStreamEvent{Type: ir.EventUsage, Usage: usage})
		}
	}

	return false
}

func (p *streamParser) finishStream() {
	if p.finished {
		return
	}
	p.finished = true
	finish := p.finish
	if finish == "" {
		if p.sawTool {
			finish = "tool_calls"
		} else {
			finish = "stop"
		}
	}
	p.emit(ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finish})
}

// --- 小工具 ---

func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case bool:
		return !x
	case float64:
		return x == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		if m := firstString(lookupString(x, "message"), lookupString(x, "msg")); m != "" {
			return m
		}
		b, _ := json.Marshal(x)
		return string(b)
	default:
		return fmt.Sprint(v)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
