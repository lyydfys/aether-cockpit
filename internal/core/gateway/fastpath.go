package gateway

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"aether-cockpit/internal/core/gateway/transducer"
	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/core/state"
	"aether-cockpit/internal/provider"
)

// hopByHopHeaders 是不应转发的逐跳头。
var hopByHopHeaders = map[string]bool{
	"connection":          true,
	"keep-alive":          true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
	"te":                  true,
	"trailer":             true,
	"transfer-encoding":   true,
	"upgrade":             true,
	// 流式响应长度未知，交给 chunked 编码自适应。
	"content-length": true,
}

// tryFastPath 尝试走"零损直通"通道。
//
// 触发条件（全部满足才走）：
//  1. 下游协议是 OpenAI Responses；
//  2. 没有 previous_response_id —— 有的话说明依赖网关自己维护的会话状态，
//     直通给上游会因为"上游不认识这个 resp_id"直接 400；
//  3. 目标渠道声明了 NativeResponses 且实现了直通能力；
//  4. 非流式请求还要求渠道显式声明 ResponsesPassthroughNonStream
//     （默认保留"非流式走 IR"的既有行为）。
//
// 走通时返回 true，调用方不要再走 IR 路径。
func (g *Gateway) tryFastPath(w http.ResponseWriter, r *http.Request, proto ir.Protocol, body []byte, entry *RequestLog, tm *requestTimer) bool {
	if proto != ir.ProtocolOpenAIResponses || g.cfg.DisableFastPath {
		return false
	}
	// 隐私遮蔽开启时不能直通：直通不解析请求体，没有机会替换敏感串。
	// 用 privacyState 而不是 currentPrivacyEngine：引擎初始化失败时也要挡住直通，
	// 否则 fail-closed 会被直通路径绕过去。
	if required, _ := g.privacyState(); required {
		return false
	}

	// 轻量探针：只取判断所需的最小字段，不构造完整 IR。
	// 这正是 Fast-Path 的价值——连解析都省掉。
	var probe struct {
		Model              string `json:"model"`
		Stream             bool   `json:"stream"`
		PreviousResponseID string `json:"previous_response_id"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	entry.Model = probe.Model
	if strings.TrimSpace(probe.Model) == "" {
		return false
	}
	if probe.PreviousResponseID != "" {
		// 有状态续轮必须经过状态缝合层，不能直通。
		return false
	}

	bindings := g.models.Resolve(probe.Model)
	if len(bindings) == 0 {
		log.Printf("[FASTPATH] model=%q 没有候选渠道", probe.Model)
		return false
	}
	fpCtx := withPresetRoute(r.Context(), g.presetRouteFor(probe.Model))

	exclude := make(map[string]bool, 2)
	for _, b := range bindings {
		p, ok := provider.Get(b.ProviderID)
		if !ok {
			continue
		}
		native, ok := p.(provider.NativeResponsesCapability)
		m := p.Manifest()
		if !ok || !m.NativeResponses {
			log.Printf("[FASTPATH] 跳过 %s: native=%v manifest=%v", b.ProviderID, ok, m.NativeResponses)
			continue
		}
		if !probe.Stream && !m.Capabilities.ResponsesPassthroughNonStream {
			log.Printf("[FASTPATH] 跳过 %s: 非流式请求但未声明 ResponsesPassthroughNonStream", b.ProviderID)
			continue
		}

		fpRoute := g.providerRoute(b.ProviderID)
		env, selErr := g.pool.Select(b.ProviderID, pool.SelectOptions{
			SessionKey: pool.SessionFingerprint(b.ProviderID, &ir.UnifiedChatRequest{
				Model: probe.Model,
				Items: []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: probe.Model}},
			}),
			Exclude:  exclude,
			Model:    b.UpstreamModel,
			Mode:     g.selectMode(b.ProviderID, fpRoute.Routing),
			Affinity: fpRoute.Affinity,
		})
		if selErr != nil {
			log.Printf("[FASTPATH] %s 选账号失败: %v", b.ProviderID, selErr.Message)
			continue
		}
		exclude[env.AccountID] = true

		upstreamModel := b.UpstreamModel
		if mapped := env.MapModel(upstreamModel); mapped != "" {
			upstreamModel = mapped
		}
		// Codex 订阅优先走上游 WebSocket（避免 chatgpt.com 的 HTTP 长流被截断）；
		// 握手/首帧失败才退回 HTTP 直通。
		if probe.Stream {
			if wsc, ok := p.(provider.ResponsesWSCapability); ok {
				started, werr := wsc.ResponsesWebSocketStream(fpCtx, env, body, upstreamModel, w)
				if started {
					env.MarkSuccess()
					entry.ProviderID = b.ProviderID
					entry.AccountID = env.AccountID
					entry.Status = "ok"
					entry.FastPath = true
					if tm != nil {
						tm.markFirstByte()
					}
					if werr != nil {
						entry.ErrMsg = "上游 WebSocket 中断: " + werr.Error()
					}
					return true
				}
				if werr != nil {
					log.Printf("[FASTPATH] %s 上游 WS 不可用，退回 HTTP: %v", b.ProviderID, werr)
				}
			}
		}

		resp, err := native.ResponsesPassthrough(fpCtx, env, body, upstreamModel)
		if err != nil {
			ae := ir.AsAPIError(err).WithProvider(b.ProviderID)
			log.Printf("[FASTPATH] %s 直通失败: %s", b.ProviderID, ae.Message)
			g.penalize(env, ae)
			entry.ProviderID = b.ProviderID
			entry.AccountID = env.AccountID
			entry.Status = "error"
			entry.ErrCode = ae.Code
			entry.ErrMsg = ae.Message
			if !retryable(ae) {
				return false
			}
			continue
		}

		// 上游非 2xx：先看是不是"上下文超限"——是就裁剪早期消息重试一次；
		// 否则关掉响应体，交给 IR 路径重新尝试（可能换渠道）。
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if retry, shrunk, dropped, ok := tryContextContinue(fpCtx, native, env, body, upstreamModel, resp); ok {
				log.Printf("[FASTPATH] %s 上下文超限，裁剪 %d 条早期消息后重试成功", b.ProviderID, dropped)
				body = shrunk
				resp = retry
				entry.AutoContinued = true
				entry.TrimmedItems = dropped
				entry.Items = countResponseItems(shrunk)
			} else {
				log.Printf("[FASTPATH] %s 上游返回 %d，退回 IR 路径", b.ProviderID, resp.StatusCode)
				continue
			}
		}

		env.MarkSuccess()
		entry.ProviderID = b.ProviderID
		entry.AccountID = env.AccountID
		entry.Status = "ok"
		entry.FastPath = true

		if tm != nil {
			tm.markFirstByte()
		}
		if probe.Stream {
			if err := relayStream(w, resp); err != nil {
				// 已经开始写响应头，无法再降级，只能记录。
				entry.ErrMsg = "直流转发中断: " + err.Error()
			}
			return true
		}

		// 非流式：整段读出再原样转发（体积有限）。顺手把响应 id 记进状态库，
		// 这样 GET /v1/responses/{id} 有据可查，后续 previous_response_id
		// 续轮也能查到它属于哪个渠道/账号（正文仍按上游字节原样返回）。
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if rerr != nil {
			entry.ErrMsg = "读取上游响应失败: " + rerr.Error()
			transducer.WriteError(w, ir.ProtocolOpenAIResponses,
				ir.NewAPIError(ir.ErrCodeUpstreamFailed, "读取上游响应失败").WithCause(rerr))
			return true
		}
		var meta struct {
			ID     string `json:"id"`
			Model  string `json:"model"`
			Output []any  `json:"output"`
		}
		_ = json.Unmarshal(raw, &meta)
		if meta.ID != "" && g.store != nil {
			g.store.PutResponse(&state.ResponseRecord{
				ResponseID: meta.ID,
				ProviderID: b.ProviderID,
				AccountID:  env.AccountID,
				Model:      meta.Model,
				Output:     meta.Output,
			})
		}
		for k, vs := range resp.Header {
			if hopByHopHeaders[strings.ToLower(k)] {
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		if _, werr := w.Write(raw); werr != nil {
			entry.ErrMsg = "直流转发中断: " + werr.Error()
		}
		return true
	}
	return false
}

// relayStream 把上游响应原样中继给下游。
//
// 不做任何解析或改写：这是"零损"的定义——上游吐什么字节，客户端就收到什么字节。
func relayStream(w http.ResponseWriter, resp *http.Response) error {
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		if hopByHopHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	// 上游可能没声明 Content-Type，兜底成 SSE。
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	}

	w.WriteHeader(resp.StatusCode)

	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
