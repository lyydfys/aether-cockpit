package pipeline

import (
	"context"
	"encoding/json"
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
// 它是最外层中间件（在 spec_overrides 之前）：先脱敏再让其它中间件动内容，
// 这样 spec 注入的系统提示词也会被一并脱敏；响应方向则相反，最后才还原，
// 保证用户看到的是原文。
//
// 为什么默认挂在所有渠道上而不是让渠道声明：
// 这是用户的隐私设置，不是渠道的协议特性。渠道不需要知道它的存在。
//
// 覆盖范围（与 gateway/privacy.go 的 maskRequest 对齐）：
// 系统指令、消息正文、工具调用参数、工具执行结果、附件文件名。
// 不覆盖附件 URL 与 base64 正文——遮蔽它们会让上游取不到资源。
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

			// 同一次请求共用 key，脱敏页才能把多段文本归为一条记录。
			key := ""
			agent := ""
			blockedBy := ""
			var blockedHits []redact.Hit

			apply := func(text, role string) string {
				if text == "" {
					return text
				}
				res := eng.MaskWithMeta(text, providerID, model, key, agent, role)
				if res.Blocked {
					blockedBy = role
					blockedHits = append(blockedHits, res.Hits...)
				}
				return res.Text
			}

			if req != nil {
				key = eng.NewRequestKey()
				if a, ok := req.Meta["client_agent"].(string); ok {
					agent = a
				}
				req.Instructions = apply(req.Instructions, "system")
				for i := range req.Items {
					it := &req.Items[i]
					role := it.Role
					if role == "" {
						role = string(it.Type)
					}
					it.Text = apply(it.Text, role)
					// 工具调用参数本身是一段 JSON 字符串：先解成结构再逐层脱敏，
					// 否则占位符里的 "{{" 会把 JSON 弄坏。
					it.Arguments = maskArguments(eng, it.Arguments, providerID, model, key, agent, role, &blockedBy, &blockedHits)
					it.Output = apply(it.Output, role+":output")
					for j := range it.Attachments {
						it.Attachments[j].FileName = apply(it.Attachments[j].FileName, role+":file")
					}
				}
			}

			// 动作被设成 block 的规则命中：整个请求不发送。
			// 空占位符已经被写回正文，所以即便这条判定被误删也不会明文外泄；
			// 但拒绝发送仍是正确语义（用户明确要求"拦截"）。
			if blockedBy != "" {
				msg := "请求命中脱敏规则（处置方式为拦截），已拒绝发送"
				if len(blockedHits) > 0 {
					var names []string
					for _, h := range blockedHits {
						if len(names) < 3 {
							names = append(names, h.Kind)
						}
					}
					if len(names) > 0 {
						msg += "：" + strings.Join(names, "、")
					}
				}
				return nil, ir.NewAPIError(ir.ErrCodePrivacyBlocked, msg).WithProvider(providerID)
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
					// 参数是 JSON 文本：这里的尾巴同样要转义，与逐块路径保持一致。
					ev.ArgsDelta = eng.UnmaskJSON(pendingArgs)
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
					ev.TextDelta = unmaskChunk(eng, key, &pendingText, ev.TextDelta, false, false)
					ev.ArgsDelta = unmaskChunk(eng, key, &pendingArgs, ev.ArgsDelta, false, true)
					out <- ev
				}
				flushPending()
			}()
			return out, nil
		}
	}
}

// maskArguments 脱敏工具调用参数。
//
// Arguments 是上游/客户端传的 JSON 字符串。直接在字符串上跑正则，占位符里的
// "{{" / "}}" 一旦落进字符串字面量还好（会被转义），但落进键名或数字位置就会
// 破坏 JSON。所以先解成 any，再对每个字符串叶子脱敏，最后重新编码；
// 解析失败时退回"整串当文本脱敏"，宁可换编码也不要漏脱敏。
func maskArguments(eng *redact.Engine, raw, providerID, model, key, agent, role string, blockedBy *string, blockedHits *[]redact.Hit) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		res := eng.MaskWithMeta(raw, providerID, model, key, agent, role)
		if res.Blocked && *blockedBy == "" {
			*blockedBy = role
		}
		if res.Blocked {
			*blockedHits = append(*blockedHits, res.Hits...)
		}
		return res.Text
	}

	var walk func(node any) any
	walk = func(node any) any {
		switch t := node.(type) {
		case string:
			res := eng.MaskWithMeta(t, providerID, model, key, agent, role)
			if res.Blocked {
				if *blockedBy == "" {
					*blockedBy = role
				}
				*blockedHits = append(*blockedHits, res.Hits...)
			}
			return res.Text
		case []any:
			for i := range t {
				t[i] = walk(t[i])
			}
			return t
		case map[string]any:
			for k := range t {
				t[k] = walk(t[k])
			}
			return t
		default:
			return node
		}
	}

	out, err := json.Marshal(walk(v))
	if err != nil {
		// 编码失败几乎不可能（输入本来就能解），退回原文避免破坏请求。
		return raw
	}
	return string(out)
}

// unmaskChunk 把 delta 接到 pending 上，返回可以安全发出的前缀。
//
// 末尾若是未闭合的 "{{"（占位符可能还没传完），把它留在 pending 里等下一个
// chunk；final 时全部发出。pending 超过 4KB 视为普通文本，避免一直攒着。
//
// asJSON 为真时用 Engine.UnmaskJSON（原值做 JSON 转义）：工具参数是 JSON 文本，
// 原值里的引号/反斜杠不转义就会拼出非法 JSON。
func unmaskChunk(eng *redact.Engine, key string, pending *string, delta string, final, asJSON bool) string {
	unmask := eng.Unmask
	if asJSON {
		unmask = eng.UnmaskJSON
	}
	if delta == "" && !final {
		return ""
	}
	buf := *pending + delta
	if !final {
		if len(buf) > 4096 {
			*pending = ""
			return unmask(buf)
		}
		if i := strings.LastIndex(buf, "{{"); i >= 0 && strings.LastIndex(buf, "}}") < i {
			*pending = buf[i:]
			eng.NoteBack(key, buf[:i])
			return unmask(buf[:i])
		}
		// 也可能正好断在第一个 "{"：留到下一块凑成 "{{"。
		if strings.HasSuffix(buf, "{") {
			*pending = "{"
			eng.NoteBack(key, buf[:len(buf)-1])
			return unmask(buf[:len(buf)-1])
		}
	}
	*pending = ""
	eng.NoteBack(key, buf)
	return unmask(buf)
}

// 保证 redact 包被引用（Deps 里用它的类型）。
var _ = redact.ActionMask
