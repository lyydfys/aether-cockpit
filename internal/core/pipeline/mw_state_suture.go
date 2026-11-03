package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/state"
	"aether-cockpit/internal/provider"
)

// stateDrainGrace 是客户端断流后继续读上游、把状态落库的上限。
const stateDrainGrace = 25 * time.Second

func init() {
	Register("stateful_session", func(deps Deps) Middleware { return StateSuture(deps) })
}

// StateSuture 是状态缝合层。
//
// 它把三个原本分散的问题合并成一处解决——因为它们的本质完全相同：
// "下游是无状态的，上游是有状态的，网关必须替两边记住状态"。
//
//	① previous_response_id 寻址：瞬间定位原账号 + 原上游会话，实现 100% 精准粘性；
//	② 历史回填：客户端只传增量时，把服务端保存的全量上下文拼回去；
//	③ 签名回缝：把上一轮上游吐出的 thoughtSignature 按 call_id 缝回本轮请求。
//
// 中间件只负责"读"，写入由同一中间件在流结束时完成（它能看到完整输出）。
func StateSuture(deps Deps) Middleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
			work := req.Clone()
			if work.Meta == nil {
				work.Meta = make(map[string]any, 4)
			}

			// ① + ②：根据 previous_response_id 回填。
			var baseItems []ir.UnifiedItem
			if work.PreviousResponseID != "" && deps.State != nil {
				if rec, ok := deps.State.GetResponse(work.PreviousResponseID); ok {
					baseItems = rec.AccumulatedItems
					if rec.ProviderID != "" {
						work.Meta["pinned_provider"] = rec.ProviderID
					}
					if rec.AccountID != "" {
						work.Meta["pinned_account"] = rec.AccountID
					}
					if rec.UpstreamSessionID != "" {
						work.Meta["upstream_session"] = rec.UpstreamSessionID
					}
					// 关键判定：客户端到底传的是"增量"还是"全量"。
					// 盲目拼接会造成历史重复，模型会看到两遍同样的对话。
					if !looksLikeFullHistory(work.Items, baseItems) {
						merged := make([]ir.UnifiedItem, 0, len(baseItems)+len(work.Items))
						merged = append(merged, baseItems...)
						merged = append(merged, work.Items...)
						work.Items = merged
					}
				} else {
					// 引用了网关里不存在（或已过期）的上一轮。
					//
					// 不能静默跳过：那样上游只看到本轮的新输入，模型"突然失忆"，
					// 客户端拿不到任何信号。显式报 previous_response_not_found
					// （与 OpenAI 同名），Codex 一类客户端收到后会重开连接、
					// 把整段会话重发，从而自愈。
					return nil, ir.NewAPIErrorf(ir.ErrCodePreviousResponseNotFound,
						"previous_response_id %q 在本网关不存在或已过期，请重发完整会话",
						work.PreviousResponseID).WithProvider(work.ProviderID)
				}
			}

			// ③：签名回缝。
			if deps.State != nil {
				restoreSignatures(deps.State, work)
			}

			// 会话指纹：用于渠道内部复用上游 session。
			if work.SessionKey == "" {
				work.SessionKey = computeSessionKey(work, env)
			}

			in, err := next(ctx, env, work)
			if err != nil {
				return nil, err
			}

			respID, _ := work.Meta["response_id"].(string)
			storeEnabled := work.StoreResponse

			out := make(chan ir.UnifiedStreamEvent, 32)
			go func() {
				defer close(out)
				acc := ir.NewAccumulator()
				var upstreamSession string

				absorb := func(ev ir.UnifiedStreamEvent) {
					if ev.Type == ir.EventStart && ev.ResponseID != "" {
						upstreamSession = ev.ResponseID
					}
					// 上游吐出的思考签名立刻入库，供下一轮回缝。
					if ev.ThoughtSignature != "" && ev.ToolCallID != "" && deps.State != nil {
						deps.State.PutSignature(ev.ToolCallID, ev.ThoughtSignature)
					}
					acc.Push(ev)
				}
				finish := func() {
					persistResponse(deps, work, respID, upstreamSession, acc, storeEnabled)
				}
				// 客户端断流后仍把上游读完再落状态：否则这一轮会话在下一轮
				// previous_response_id 续接时找不到，等于把会话丢了。
				drain := func() {
					deadline := time.NewTimer(stateDrainGrace)
					defer deadline.Stop()
					for {
						select {
						case ev, ok := <-in:
							if !ok {
								finish()
								return
							}
							absorb(ev)
						case <-deadline.C:
							finish()
							return
						}
					}
				}

				for {
					select {
					case <-ctx.Done():
						drain()
						return
					case ev, ok := <-in:
						if !ok {
							finish()
							return
						}
						absorb(ev)
						select {
						case out <- ev:
						case <-ctx.Done():
							drain()
							return
						}
					}
				}
			}()
			return out, nil
		}
	}
}

// persistResponse 把本轮完整上下文写入状态存储。
//
// store 是客户端的 "store" 字段，它只说"**上游**要不要保留这条 response"，
// 不是"网关能不能记上下文"。所以两种情况下都要落库，只是外部可见性不同：
//   - store=true：正常快照，GET /v1/responses/{id} 可以回放；
//   - store=false：仍然记账（否则上一轮之后的 previous_response_id 必然查不到），
//     但标记 Internal，不对外提供回放。
func persistResponse(deps Deps, req *ir.UnifiedChatRequest, respID, upstreamSession string, acc *ir.Accumulator, store bool) {
	if deps.State == nil || respID == "" {
		return
	}
	res := acc.Result()

	items := make([]ir.UnifiedItem, 0, len(req.Items)+len(res.ToolCalls)+2)
	items = append(items, req.Items...)
	items = append(items, assistantItems(res)...)

	rec := &state.ResponseRecord{
		ResponseID:        respID,
		PreviousID:        req.PreviousResponseID,
		ProviderID:        req.ProviderID,
		AccountID:         accountIDOf(req),
		UpstreamSessionID: upstreamSession,
		Model:             req.Model,
		AccumulatedItems:  items,
		Internal:          !store,
	}
	deps.State.PutResponse(rec)
}

// assistantItems 把聚合结果转成可回填的 IR item。
func assistantItems(res ir.AggregatedResponse) []ir.UnifiedItem {
	var out []ir.UnifiedItem

	if strings.TrimSpace(res.ReasoningText) != "" {
		it := ir.UnifiedItem{
			Type: ir.ItemTypeReasoning,
			Role: "assistant",
			Text: res.ReasoningText,
		}
		// 若本轮只有一个工具调用，签名归属明确，直接挂上。
		if len(res.ToolCalls) == 1 && res.ThoughtSignatures != nil {
			it.ThoughtSignature = res.ThoughtSignatures[res.ToolCalls[0].ID]
		}
		out = append(out, it)
	}
	if strings.TrimSpace(res.Text) != "" {
		out = append(out, ir.UnifiedItem{
			Type: ir.ItemTypeMessage,
			Role: "assistant",
			Text: res.Text,
		})
	}
	for _, tc := range res.ToolCalls {
		out = append(out, ir.UnifiedItem{
			Type:      ir.ItemTypeFunctionCall,
			Role:      "assistant",
			ID:        tc.ID,
			CallID:    tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		})
	}
	return out
}

// accountIDOf 从 Meta 里取被锁定的账号（由路由层写入）。
func accountIDOf(req *ir.UnifiedChatRequest) string {
	if v, ok := req.Meta["account_id"].(string); ok {
		return v
	}
	return ""
}

// looksLikeFullHistory 判断客户端是否已经把完整历史带过来了。
//
// 判据：请求里第一条用户文本与快照里第一条用户文本一致，说明是从头开始的完整会话。
// 增量请求的首项通常是 function_call_output，因此不会误判。
func looksLikeFullHistory(reqItems, recItems []ir.UnifiedItem) bool {
	a := firstUserText(reqItems)
	b := firstUserText(recItems)
	if a == "" || b == "" {
		return false
	}
	return a == b
}

func firstUserText(items []ir.UnifiedItem) string {
	for _, it := range items {
		if it.IsUserTurn() && strings.TrimSpace(it.Text) != "" {
			return strings.TrimSpace(it.Text)
		}
	}
	return ""
}

// restoreSignatures 把缓存的思考签名缝回请求。
//
// 两种情形：
//   - 客户端回传了完整的 function_call item：直接补 ThoughtSignature 字段；
//   - 客户端只回了 function_call_output（多数 OpenAI 客户端如此）：在它前面补一个
//     带签名的 function_call item，否则 Gemini/Claude 系上游会以 400 拒绝。
func restoreSignatures(st *state.Store, req *ir.UnifiedChatRequest) {
	type pending struct {
		idx  int
		call ir.UnifiedItem
	}
	var inserts []pending

	for i := range req.Items {
		it := req.Items[i]
		if it.Type != ir.ItemTypeFunctionCallOutput || it.CallID == "" {
			continue
		}
		sig, ok := st.GetSignature(it.CallID)
		if !ok {
			continue
		}

		attached := false
		for j := range req.Items {
			if req.Items[j].Type == ir.ItemTypeFunctionCall && req.Items[j].CallID == it.CallID {
				req.Items[j].ThoughtSignature = sig
				attached = true
				break
			}
		}
		if attached {
			continue
		}
		inserts = append(inserts, pending{
			idx: i,
			call: ir.UnifiedItem{
				Type:             ir.ItemTypeFunctionCall,
				Role:             "assistant",
				CallID:           it.CallID,
				Name:             it.Name,
				Arguments:        "{}",
				ThoughtSignature: sig,
			},
		})
	}

	// 从后往前插入，避免下标位移。
	for i := len(inserts) - 1; i >= 0; i-- {
		p := inserts[i]
		req.Items = append(req.Items, ir.UnifiedItem{})
		copy(req.Items[p.idx+1:], req.Items[p.idx:])
		req.Items[p.idx] = p.call
	}
}

// computeSessionKey 计算稳定的会话指纹。
//
// 取最近两条用户消息 + 首条用户消息一起哈希：
// 只用最近一条会在"重复提问"时误命中，加首条可以显著降低碰撞。
func computeSessionKey(req *ir.UnifiedChatRequest, env *provider.AccountEnv) string {
	h := sha256.New()
	if env != nil {
		h.Write([]byte(env.ProviderID))
		h.Write([]byte{0})
		h.Write([]byte(env.AccountID))
		h.Write([]byte{0})
	}
	h.Write([]byte(firstUserText(req.Items)))
	h.Write([]byte{0})

	var userTexts []string
	for _, it := range req.Items {
		if it.IsUserTurn() && strings.TrimSpace(it.Text) != "" {
			userTexts = append(userTexts, strings.TrimSpace(it.Text))
		}
	}
	if n := len(userTexts); n > 0 {
		h.Write([]byte(userTexts[n-1]))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
