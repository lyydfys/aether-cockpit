package pipeline

import (
	"context"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/redact"
	"aether-cockpit/internal/provider"
)

func init() {
	Register("redact", func(deps Deps) Middleware { return Redact(deps) })
}

// Redact 在请求离开本机前脱敏，在响应回来时还原。
//
// 它是最外层中间件（在 spec_overrides 之前）：先脱敏再让其他中间件动内容，
// 这样 spec 注入的系统提示词也会被一并脱敏；响应方向则相反，最后才还原，
// 保证用户看到的是原文。
//
// 为什么默认挂在所有渠道上而不是让渠道声明：
// 这是用户的隐私设置，不是渠道的协议特性。渠道不需要知道它的存在。
func Redact(deps Deps) Middleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
			eng := deps.Redact
			if eng == nil || !eng.Enabled() {
				return next(ctx, env, req)
			}

			providerID, model := "", ""
			if req != nil {
				providerID = req.ProviderID
				model = req.Model
			}

			// 请求方向：Instructions 与每条 item 的文本。
			// 同一次请求共用 key，脱敏页才能把多段文本归为一条记录。
			key := ""
			agent := ""
			if req != nil {
				key = eng.NewRequestKey()
				if a, ok := req.Meta["client_agent"].(string); ok {
					agent = a
				}
				if req.Instructions != "" {
					req.Instructions = eng.MaskWithMeta(req.Instructions, providerID, model, key, agent, "system").Text
				}
				for i := range req.Items {
					it := &req.Items[i]
					if it.Text != "" {
						role := it.Role
						if role == "" {
							role = string(it.Type)
						}
						it.Text = eng.MaskWithMeta(it.Text, providerID, model, key, agent, role).Text
					}
				}
			}

			ch, err := next(ctx, env, req)
			if err != nil {
				return nil, err
			}

			// 响应方向：把占位符换回原值。
			// 占位符可能被上游拆到多个 chunk（"{{EMAIL_" / "ab12}}"），
			// 必须跨 chunk 缓冲，否则用户会看到半截占位符。
			out := make(chan ir.UnifiedStreamEvent, 8)
			go func() {
				defer close(out)
				var pendingText, pendingArgs string
				flushPending := func() {
					if pendingText == "" && pendingArgs == "" {
						return
					}
					ev := ir.UnifiedStreamEvent{Type: ir.EventTextDelta}
					ev.TextDelta = eng.Unmask(pendingText)
					ev.ArgsDelta = eng.Unmask(pendingArgs)
					pendingText, pendingArgs = "", ""
					out <- ev
				}
				for ev := range ch {
					if ev.TextDelta == "" && ev.ArgsDelta == "" {
						// done / usage 等边界事件：先把攒着的半截占位符发出去，
						// 再发边界事件，保证拼接方不会漏掉尾巴。
						flushPending()
						out <- ev
						continue
					}
					ev.TextDelta = unmaskChunk(eng, key, &pendingText, ev.TextDelta, false)
					ev.ArgsDelta = unmaskChunk(eng, key, &pendingArgs, ev.ArgsDelta, false)
					out <- ev
				}
				flushPending()
			}()
			return out, nil
		}
	}
}

// unmaskChunk 把 delta 接到 pending 上，返回可以安全发出的前缀。
//
// 末尾若是未闭合的 "{{"（占位符可能还没传完），把它留在 pending 里等下一个
// chunk；final 时全部发出。pending 超过 4KB 视为普通文本，避免一直攒着。
func unmaskChunk(eng *redact.Engine, key string, pending *string, delta string, final bool) string {
	if delta == "" && !final {
		return ""
	}
	buf := *pending + delta
	if !final {
		if len(buf) > 4096 {
			*pending = ""
			return eng.Unmask(buf)
		}
		if i := strings.LastIndex(buf, "{{"); i >= 0 && strings.LastIndex(buf, "}}") < i {
			*pending = buf[i:]
			eng.NoteBack(key, buf[:i])
			return eng.Unmask(buf[:i])
		}
		// 也可能正好断在第一个 "{"：留到下一块凑成 "{{"。
		if strings.HasSuffix(buf, "{") {
			*pending = "{"
			eng.NoteBack(key, buf[:len(buf)-1])
			return eng.Unmask(buf[:len(buf)-1])
		}
	}
	*pending = ""
	eng.NoteBack(key, buf)
	return eng.Unmask(buf)
}

// 保证 redact 包被引用（Deps 里用它的类型）。
var _ = redact.ActionMask
