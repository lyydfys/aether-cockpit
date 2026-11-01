package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func init() {
	Register("xml_tool_call", func(deps Deps) Middleware { return XMLToolCall() })
}

// xmlOpenTags 是支持的工具调用开标签。多别名是为了对接不同上游的既有习惯。
var xmlOpenTags = []string{"<tool_call>", "<tool_use>", "<function_call>"}

var xmlCloseTagOf = map[string]string{
	"<tool_call>":     "</tool_call>",
	"<tool_use>":      "</tool_use>",
	"<function_call>": "</function_call>",
}

// XMLToolCall 为不支持原生 tools（Function Calling）的上游补上工具能力。
//
//	入站：把 Tools 序列化成 XML 提示词注入 Instructions，并清空原生 Tools。
//	出站：用流式有限状态机扫描文本，一旦命中完整的工具调用块，
//	      立即截断文本流并转为标准 ToolCall 事件。
//
// 这是"一次编写、所有免费渠道免费复用"的典型中间件：
// 新渠道只要在 Manifest 里声明 "xml_tool_call"，就凭空获得了驱动
// Claude Code / Cursor 这类 Agent 客户端的能力。
func XMLToolCall() Middleware {
	return func(next StreamHandler) StreamHandler {
		return func(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
			work := req.Clone()

			hasTools := len(work.Tools) > 0
			if hasTools {
				work.Instructions = strings.TrimSpace(work.Instructions + "\n\n" + renderToolPrompt(work.Tools))
				// 上游认不出原生 tools 字段，留着反而可能被拒绝。
				work.Tools = nil
			}

			in, err := next(ctx, env, work)
			if err != nil {
				return nil, err
			}
			if !hasTools {
				return in, nil
			}

			out := make(chan ir.UnifiedStreamEvent, 32)
			go func() {
				defer close(out)
				sc := newXMLScanner()
				callSeq := 0
				totalCalls := 0
				doneSent := false

				emitText := func(text string) bool {
					if text == "" {
						return true
					}
					select {
					case out <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: text}:
						return true
					case <-ctx.Done():
						return false
					}
				}

				emitCalls := func(calls []parsedToolCall) bool {
					for _, c := range calls {
						callSeq++
						totalCalls++
						id := c.ID
						if id == "" {
							id = fmt.Sprintf("call_xml_%d", callSeq)
						}
						if !push(ctx, out, ir.UnifiedStreamEvent{
							Type:       ir.EventToolCallStart,
							ToolCallID: id,
							ToolName:   c.Name,
						}) {
							return false
						}
						if !push(ctx, out, ir.UnifiedStreamEvent{
							Type:       ir.EventToolCallDelta,
							ToolCallID: id,
							ToolName:   c.Name,
							ArgsDelta:  c.Arguments,
						}) {
							return false
						}
					}
					return true
				}

				for {
					select {
					case <-ctx.Done():
						return
					case ev, ok := <-in:
						if !ok {
							// 上游没发 Done 就关流：兜底收尾。
							if !doneSent {
								text, calls := sc.Flush()
								if !emitText(text) {
									return
								}
								if !emitCalls(calls) {
									return
								}
								if !push(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: finishReasonFor(totalCalls)}) {
									return
								}
							}
							return
						}

						if ev.Type != ir.EventTextDelta {
							// 收到 Done 时先把扫描器里残留的内容吐完，再改写结束原因。
							if ev.Type == ir.EventDone {
								text, calls := sc.Flush()
								if !emitText(text) {
									return
								}
								if !emitCalls(calls) {
									return
								}
								ev.FinishReason = finishReasonFor(totalCalls)
								doneSent = true
							}
							if !push(ctx, out, ev) {
								return
							}
							continue
						}

						text, calls := sc.Feed(ev.TextDelta)
						if !emitText(text) {
							return
						}
						if !emitCalls(calls) {
							return
						}
					}
				}
			}()
			return out, nil
		}
	}
}

func push(ctx context.Context, out chan<- ir.UnifiedStreamEvent, ev ir.UnifiedStreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// finishReasonFor 根据是否产生过工具调用决定结束原因。
func finishReasonFor(calls int) string {
	if calls > 0 {
		return "tool_calls"
	}
	return "stop"
}

// renderToolPrompt 把工具定义渲染成注入用的系统提示词。
func renderToolPrompt(tools []ir.UnifiedToolSpec) string {
	var sb strings.Builder
	sb.WriteString("你具备调用外部工具的能力。当你需要调用工具时，必须在回答中输出如下格式的代码块，且不要在其前后添加额外解释：\n\n")
	sb.WriteString("<tool_call>\n")
	sb.WriteString(`{"name": "工具名", "arguments": {"参数名": "参数值"}}`)
	sb.WriteString("\n</tool_call>\n\n")
	sb.WriteString("你需要时可以连续输出多个 <tool_call> 块。arguments 必须是合法 JSON 对象。\n")
	sb.WriteString("如果不需要调用工具，请直接用自然语言回答，绝不要输出 <tool_call> 标签。\n\n")
	sb.WriteString("可用工具列表：\n<tools>\n")
	for _, t := range tools {
		sb.WriteString("<tool name=\"")
		sb.WriteString(t.Name)
		sb.WriteString("\">\n")
		if t.Description != "" {
			sb.WriteString("说明：")
			sb.WriteString(t.Description)
			sb.WriteString("\n")
		}
		if len(t.Parameters) > 0 {
			sb.WriteString("参数 Schema：")
			sb.WriteString(string(t.Parameters))
			sb.WriteString("\n")
		}
		sb.WriteString("</tool>\n")
	}
	sb.WriteString("</tools>")
	return sb.String()
}

// --- 流式扫描状态机 ---

const (
	xmlStateText = iota
	xmlStateInTool
)

// xmlMaxBuffer 是单个工具块的最大缓冲。超过则判定为"模型跑飞"，按普通文本吐出。
const xmlMaxBuffer = 256 << 10

// xmlFenceMinRun 是 Markdown 围栏标记的最短长度（``` 或 ~~~）。
const xmlFenceMinRun = 3

type parsedToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type xmlScanner struct {
	buf      string
	state    int
	closeTag string
	// Markdown 围栏感知状态（借鉴 aether mdCode.find），跨 chunk 保持：
	// 弱渠道模型爱把 <tool_call> 的用法示例复述进 ``` 代码块，围栏内的
	// 伪标签必须当普通文本输出，不能进状态机。
	inFence   bool
	fenceChar byte // 围栏标记字符：'`' 或 '~'，闭合行必须同字符才配对
	fenceMin  int  // 闭合标记串的最短长度（不小于开口长度）
	// lineHead 表示"下一个待处理字符位于行首，且本行至今只出现过空白"。
	// 围栏标记只认这种位置；空白本身照常吐出，上下文由该标志延续。
	lineHead bool
}

func newXMLScanner() *xmlScanner {
	// 文档开头即行首。
	return &xmlScanner{state: xmlStateText, lineHead: true}
}

// Feed 消费一个文本片段，返回可以立即下发的文本与已完成的工具调用。
func (s *xmlScanner) Feed(delta string) (string, []parsedToolCall) {
	if delta == "" {
		return "", nil
	}
	s.buf += delta

	var (
		out   strings.Builder
		calls []parsedToolCall
	)

	for {
		switch s.state {

		case xmlStateText:
			if s.scanText(&out) {
				return out.String(), calls
			}
			// scanText 命中围栏外的完整开标签并转入工具态，继续消化缓冲。

		case xmlStateInTool:
			idx := strings.Index(s.buf, s.closeTag)
			if idx < 0 {
				if len(s.buf) > xmlMaxBuffer {
					// 明显不是工具调用，把内容原样吐出，恢复文本态。
					out.WriteString(s.buf)
					s.buf = ""
					s.state = xmlStateText
				}
				return out.String(), calls
			}
			payload := s.buf[:idx]
			s.buf = s.buf[idx+len(s.closeTag):]
			s.state = xmlStateText
			if call, ok := parseToolPayload(payload); ok {
				calls = append(calls, call)
			}
		}
	}
}

// scanText 在文本态扫描 s.buf，区分"普通文本 / 围栏内文本 / 工具调用开标签"。
//
// 返回 true 表示本轮扫描结束（缓冲耗尽或扣尾等待更多输入）；
// 返回 false 表示命中了围栏外的完整开标签，已切换到工具态，调用方继续循环。
//
// 围栏规则（对齐 aether mdCode.find 的思路）：
//   - 行首（允许前置空白）连续 xmlFenceMinRun 个及以上 ` 或 ~ 开启围栏，
//     同一行其余部分是 info string；
//   - 只有与开口标记同字符、长度不小于开口长度的"纯标记行"
//     （前后只允许空白）才闭合围栏；
//   - 围栏内的 <tool_call> 等标签一律当普通文本，不进入状态机；
//   - 围栏状态跨 chunk 保持。与"半个标签扣尾"协同：被扣住的尾巴若最终
//     落进围栏（或被证明既不是标签也不是围栏标记），会原样按文本吐出。
func (s *xmlScanner) scanText(out *strings.Builder) bool {
	resumedFromFence := false
	if s.inFence {
		s.scanFence(out)
		if s.inFence {
			return true
		}
		resumedFromFence = true
	}
	buf := s.buf
	p := 0
	// head：p 所在行是否仍是"行首 + 纯空白前缀"。围栏标记只认这种位置。
	// 刚从围栏里出来时，闭合行以 \n 结尾，下一字符必在行首。
	head := s.lineHead || resumedFromFence
	for {
		if head {
			q := p
			for q < len(buf) && isFenceWS(buf[q]) {
				q++
			}
			if q == len(buf) {
				// 剩余全是空白、行未定型：空白照常吐出，
				// "本行仍是空白行首"的上下文由 s.lineHead 延续到下一 chunk。
				out.WriteString(buf[:len(buf)])
				s.buf = ""
				s.lineHead = true
				return true
			}
			if c := buf[q]; c == '`' || c == '~' {
				r := q
				for r < len(buf) && buf[r] == c {
					r++
				}
				switch {
				case r-q >= xmlFenceMinRun:
					// 围栏打开：标记与 info string 本身仍是普通文本。
					out.WriteString(buf[:q])
					s.inFence = true
					s.fenceChar = c
					s.fenceMin = r - q
					s.buf = buf[q:]
					s.consumeFenceOpener(out)
					s.scanFence(out)
					if s.inFence {
						s.lineHead = false // 围栏内行首判定由 scanFence 自理
						return true
					}
					// 围栏在本轮缓冲内闭合：从闭合行之后继续文本扫描。
					buf = s.buf
					p = 0
					head = true
					continue
				case r < len(buf):
					// 标记串不足 3 且被其它字符打断：本行定死不是围栏行。
					p = r
					head = false
					continue
				default:
					// 标记串可能仍在增长（"``" → "```"）：只扣住标记串本身，
					// 前面的空白照常吐出。后续若凑满即开围栏，凑不满按文本吐出。
					out.WriteString(buf[:q])
					s.buf = buf[q:]
					s.lineHead = true
					return true
				}
			}
			// 行首出现其它字符：本行不可能是围栏行。
			p = q
			head = false
		}

		// 行内找完整开标签。标签字面量不含 \n，逐行查找与全缓冲查找等价。
		limit := len(buf)
		if nl := strings.IndexByte(buf[p:], '\n'); nl >= 0 {
			limit = p + nl
		}
		idx, tag := findOpenTag(buf[p:limit])
		if idx >= 0 {
			out.WriteString(buf[:p+idx])
			s.buf = buf[p+idx+len(tag):]
			s.closeTag = xmlCloseTagOf[tag]
			s.state = xmlStateInTool
			s.lineHead = false
			return false
		}
		if limit < len(buf) {
			// 本行剩余全是普通文本；换行后进入下一行（回到行首判定）。
			p = limit + 1
			head = true
			continue
		}
		// 缓冲耗尽且无完整标签：沿用"半个标签扣尾"逻辑。
		safe, rest := splitAtPartialOpenTag(buf[p:])
		out.WriteString(buf[:p+len(safe)])
		s.buf = rest
		s.lineHead = false
		return true
	}
}

// consumeFenceOpener 消费围栏开口行本身（标记 + info string），
// 避免开口行被 scanFence 误判成闭合行。开口行内容仍是普通文本。
func (s *xmlScanner) consumeFenceOpener(out *strings.Builder) {
	if nl := strings.IndexByte(s.buf, '\n'); nl >= 0 {
		out.WriteString(s.buf[:nl+1])
		s.buf = s.buf[nl+1:]
		return
	}
	// 开口行还没结束：info string 是普通文本，先吐出，行尾交给后续 chunk。
	out.WriteString(s.buf)
	s.buf = ""
}

// scanFence 在围栏内扫描：所有内容（含伪标签）都是普通文本，
// 直到出现与开口标记配对的闭合行为止。
func (s *xmlScanner) scanFence(out *strings.Builder) {
	for s.inFence {
		buf := s.buf
		nl := strings.IndexByte(buf, '\n')
		if nl < 0 {
			if isFenceMarkerLine(buf, s.fenceChar, 1) {
				// 形如"空白* 标记串 空白*"的未完行可能是正在写出的闭合行
				//（标记串可能继续增长、后面可能跟 \n），扣住整行等待。
				return
			}
			out.WriteString(buf)
			s.buf = ""
			return
		}
		if isFenceMarkerLine(buf[:nl], s.fenceChar, s.fenceMin) {
			// 闭合行本身也按普通文本吐出，之后恢复正常标签识别。
			out.WriteString(buf[:nl+1])
			s.inFence = false
			s.buf = buf[nl+1:]
			return
		}
		out.WriteString(buf[:nl+1])
		s.buf = buf[nl+1:]
	}
}

// isFenceMarkerLine 判断一行（不含换行符）是否为"空白* 标记串 空白*"形态。
// minRun 是标记串最短长度：闭合行要求不小于开口长度；
// 检查"可能是未写完的闭合行"时传 1（标记串还在增长）。
func isFenceMarkerLine(line string, fenceChar byte, minRun int) bool {
	i := 0
	for i < len(line) && isFenceWS(line[i]) {
		i++
	}
	j := i
	for j < len(line) && line[j] == fenceChar {
		j++
	}
	if j-i < minRun {
		return false
	}
	for k := j; k < len(line); k++ {
		if !isFenceWS(line[k]) {
			return false
		}
	}
	return true
}

// isFenceWS 判定围栏行允许的空白。不含换行符本身——它是行的终结。
func isFenceWS(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r'
}

// Flush 在流结束时吐出所有残留。
func (s *xmlScanner) Flush() (string, []parsedToolCall) {
	rest := s.buf
	s.buf = ""
	if s.state == xmlStateInTool {
		// 未闭合的工具块：按文本处理，避免静默丢内容。
		s.state = xmlStateText
		return rest, nil
	}
	// 文本态残留可能是被扣住的半个标签，直接补发。
	return rest, nil
}

// findOpenTag 在 s 中找到最早出现的开标签。
func findOpenTag(s string) (int, string) {
	best := -1
	var bestTag string
	for _, tag := range xmlOpenTags {
		if idx := strings.Index(s, tag); idx >= 0 && (best < 0 || idx < best) {
			best = idx
			bestTag = tag
		}
	}
	return best, bestTag
}

// splitAtPartialOpenTag 把末尾可能是"半个开标签"的部分切出来暂存。
//
// 例：buf = "hello <tool_ca" → ("hello ", "<tool_ca")
// 这样既不会把半个标签当正文发出去，也不会因为等待而无限延迟输出。
func splitAtPartialOpenTag(s string) (safe, rest string) {
	for _, tag := range xmlOpenTags {
		maxN := len(tag) - 1
		if maxN > len(s) {
			maxN = len(s)
		}
		for n := maxN; n > 0; n-- {
			if strings.HasSuffix(s, tag[:n]) {
				return s[:len(s)-n], s[len(s)-n:]
			}
		}
	}
	return s, ""
}

// parseToolPayload 解析一个工具调用块的内容。
//
// 兼容三种常见写法：
//
//	{"name":"x","arguments":{...}}         OpenAI 风格
//	{"name":"x","input":{...}}             Anthropic 风格
//	{"id":"call_1","name":"x","arguments":"{\"a\":1}"}  带 id + 字符串参数
func parseToolPayload(payload string) (parsedToolCall, bool) {
	p := strings.TrimSpace(payload)
	p = stripCodeFence(p)
	if p == "" {
		return parsedToolCall{}, false
	}

	var wire struct {
		ID         string          `json:"id"`
		Name       string          `json:"name"`
		Tool       string          `json:"tool"`
		Function   string          `json:"function"`
		Arguments  json.RawMessage `json:"arguments"`
		Input      json.RawMessage `json:"input"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(p), &wire); err != nil {
		return parsedToolCall{}, false
	}

	name := firstNonEmpty(wire.Name, wire.Tool, wire.Function)
	if name == "" {
		return parsedToolCall{}, false
	}

	args := firstNonEmptyRaw(wire.Arguments, wire.Input, wire.Parameters)
	return parsedToolCall{
		ID:        wire.ID,
		Name:      name,
		Arguments: normalizeArgsJSON(args),
	}, true
}

// stripCodeFence 去掉模型爱加的 ```json 包裹。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimPrefix(s, "json")
	s = strings.TrimPrefix(s, "JSON")
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// normalizeArgsJSON 把参数统一成"JSON 字符串"。
func normalizeArgsJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	// 先试字符串形态
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if strings.TrimSpace(s) == "" {
			return "{}"
		}
		return s
	}
	// 再试对象形态：压缩成单行
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	}
	return "{}"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstNonEmptyRaw(vals ...json.RawMessage) json.RawMessage {
	for _, v := range vals {
		if len(v) > 0 && string(v) != "null" {
			return v
		}
	}
	return nil
}
