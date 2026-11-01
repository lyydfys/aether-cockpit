// Package mock 提供一个完全本地、无需任何真实凭证的渠道实现。
//
// 它的作用不是"占位"，而是让整条主干在没有任何逆向协议数据的情况下
// 也能端到端跑通并验证：
//   - 三套下游协议解析 → IR → 中间件 → 三套出站 transducer 是否闭环；
//   - previous_response_id 状态缝合是否真的生效；
//   - xml_tool_call 的流式 FSM 能否正确截断文本并产出工具调用。
//
// 真实渠道的实现可以照抄这里的结构，只把"造数据"换成"发 HTTP 请求"。
package mock

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// nativeProvider 模拟一个"能力完整"的上游：支持原生 tools、支持思考块、有状态会话。
type nativeProvider struct {
	// usage 记录签到状态，仅用于演示。
	checked bool
}

func (p *nativeProvider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:             "mock-native",
		DisplayName:    "Mock 原生渠道",
		Family:         "mock",
		RegionTag:      "LOCAL",
		BrandColor:     "#6366F1",
		Description:    "本地回显渠道，支持原生工具调用与思考块。用于验证协议转换与状态缝合，无需任何真实凭证。",
		NetworkPolicy:  provider.NetPolicySystem,
		SupportedAuths: []provider.AuthMethod{provider.AuthManualInput},
		AuthFormSchema: []provider.FormFieldSchema{
			{Key: "token", Label: "任意字符串", Type: "text", Placeholder: "留空也可，仅用于演示", Required: true},
		},
		Tasks: []provider.TaskDefinition{
			{TaskID: "daily_checkin", Title: "每日签到（模拟）", CronDefault: "0 9 * * *"},
			{TaskID: "bonus_task", Title: "额外任务（模拟）", CronDefault: "0 10 * * *", DependsOn: []string{"daily_checkin"}, Optional: true},
		},
		Models: []ir.ModelMeta{
			{
				ID:            "mock-native/echo-pro",
				UpstreamModel: "echo-pro",
				DisplayName:   "Echo Pro",
				ContextWindow: 128000,
				Capabilities:  ir.ModelCapabilities{ToolCall: true, Reasoning: true, Streaming: true, Vision: true},
			},
		},
		RequiredMiddlewares: []string{"stateful_session"},
		Capabilities: provider.CapabilitySet{
			Chat:    true,
			CheckIn: true,
			Quota:   true,
		},
	}
}

// ChatStream 模拟有状态的流式对话。
//
// 刻意做了两件事来贴近真实逆向渠道：
//  1. 每次都在 EventStart 里返回一个"上游会话 ID"，供状态缝合层记录；
//  2. 分片之间有真实延迟，便于观察流式转录是否逐字下发。
func (p *nativeProvider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	reply, wantTool := buildReply(req, true)

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)

		// 模拟上游会话：真实渠道这里是新建/复用 conversation_id。
		sessionID := "sess_" + shortHash(req.SessionKey)
		if v, ok := req.Meta["upstream_session"].(string); ok && v != "" {
			sessionID = v
		}
		if !emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventStart, ResponseID: sessionID}) {
			return
		}

		// 思考块
		if req.ReasoningEffort != "" || wantTool {
			for _, chunk := range chunkString("我需要先分析一下用户的需求，再决定是否调用工具。", 12) {
				if !emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: chunk}) {
					return
				}
				sleep(ctx, 12*time.Millisecond)
			}
		}

		// 正文
		for _, chunk := range chunkString(reply, 10) {
			if !emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: chunk}) {
				return
			}
			sleep(ctx, 10*time.Millisecond)
		}

		// 工具调用
		if wantTool {
			callID := "call_mock_" + shortHash(req.RequestID)
			if !emit(ctx, out, ir.UnifiedStreamEvent{
				Type:             ir.EventToolCallStart,
				ToolCallID:       callID,
				ToolName:         req.Tools[0].Name,
				ThoughtSignature: "mock-signature-" + shortHash(callID),
			}) {
				return
			}
			args := `{"path":"main.go"}`
			for _, chunk := range chunkString(args, 6) {
				if !emit(ctx, out, ir.UnifiedStreamEvent{
					Type:       ir.EventToolCallDelta,
					ToolCallID: callID,
					ArgsDelta:  chunk,
				}) {
					return
				}
				sleep(ctx, 8*time.Millisecond)
			}
		}

		emit(ctx, out, ir.UnifiedStreamEvent{
			Type: ir.EventUsage,
			Usage: &ir.Usage{
				InputTokens:  estimateTokens(req),
				OutputTokens: len([]rune(reply))/3 + 1,
				TotalTokens:  estimateTokens(req) + len([]rune(reply))/3 + 1,
			},
		})

		reason := "stop"
		if wantTool {
			reason = "tool_calls"
		}
		emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: reason})
	}()
	return out, nil
}

// ExecuteTasks 模拟签到。
func (p *nativeProvider) ExecuteTasks(ctx context.Context, env *provider.AccountEnv, taskIDs []string) (*provider.CheckInReport, error) {
	report := &provider.CheckInReport{Success: true}
	for _, id := range taskIDs {
		start := time.Now()
		// 模拟网络往返。
		sleep(ctx, 150*time.Millisecond)

		res := provider.TaskResult{
			TaskID:    id,
			Title:     taskTitle(id),
			Success:   true,
			Points:    100,
			LatencyMS: time.Since(start).Milliseconds(),
		}
		if id == "bonus_task" {
			res.Message = "模拟：该任务偶发失败，属于可选任务"
		}
		report.EarnedPoints += res.Points
		report.Results = append(report.Results, res)
	}
	report.Message = fmt.Sprintf("签到完成，共获得 %d 积分（模拟）", report.EarnedPoints)
	env.SetQuota(&provider.QuotaStatus{
		Total:     10000,
		Used:      1000,
		Remaining: 9000,
		Unit:      "积分",
		ExpiresAt: time.Now().Add(8 * time.Hour).Unix(),
	})
	return report, nil
}

// QueryQuota 模拟额度查询。
func (p *nativeProvider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	if q := env.Quota(); q != nil {
		return q, nil
	}
	return &provider.QuotaStatus{Total: 10000, Remaining: 8800, Unit: "积分"}, nil
}

// --- 弱能力渠道 ---

// weakProvider 模拟"只认纯文本、不认原生 tools、不接受 system 角色"的免费网页端渠道。
//
// 它声明挂载 xml_tool_call + system_role_merge + stateful_session 三个中间件，
// 用来验证：即便上游什么高级能力都没有，客户端依然能正常驱动 Agent 工具调用。
type weakProvider struct{}

func (p *weakProvider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:             "mock-weak",
		DisplayName:    "Mock 弱能力渠道",
		Family:         "mock",
		RegionTag:      "LOCAL",
		BrandColor:     "#10B981",
		Description:    "模拟不支持原生 Function Calling、不接受 system 角色的免费渠道，用于验证补偿中间件是否真的生效。",
		NetworkPolicy:  provider.NetPolicySystem,
		SupportedAuths: []provider.AuthMethod{provider.AuthManualInput},
		AuthFormSchema: []provider.FormFieldSchema{
			{Key: "cookie", Label: "Cookie", Type: "textarea", Placeholder: "任意字符串即可", Required: true},
		},
		Tasks: []provider.TaskDefinition{
			{TaskID: "web_checkin", Title: "网页端签到（模拟）", CronDefault: "30 8 * * *"},
		},
		Models: []ir.ModelMeta{
			{
				ID:            "mock-weak/echo-lite",
				UpstreamModel: "echo-lite",
				DisplayName:   "Echo Lite",
				ContextWindow: 32000,
				Capabilities:  ir.ModelCapabilities{ToolCall: false, Reasoning: false, Streaming: true},
			},
		},
		// 三个补偿中间件全挂上：这正是"一次编写、所有弱渠道免费复用"的验证点。
		RequiredMiddlewares: []string{"system_role_merge", "xml_tool_call", "stateful_session"},
		Capabilities: provider.CapabilitySet{
			Chat:    true,
			CheckIn: true,
			Quota:   true,
		},
	}
}

func (p *weakProvider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	// 这个渠道"看不见" req.Tools —— xml_tool_call 中间件已经把它清空并转成了提示词。
	reply, _ := buildReply(req, false)

	out := make(chan ir.UnifiedStreamEvent, 32)
	go func() {
		defer close(out)

		emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventStart, ResponseID: "weak_" + shortHash(req.SessionKey)})

		for _, chunk := range chunkString(reply, 8) {
			if !emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: chunk}) {
				return
			}
			sleep(ctx, 9*time.Millisecond)
		}

		emit(ctx, out, ir.UnifiedStreamEvent{
			Type:  ir.EventUsage,
			Usage: &ir.Usage{InputTokens: estimateTokens(req), OutputTokens: len([]rune(reply)) / 3, TotalTokens: estimateTokens(req) + len([]rune(reply))/3},
		})
		emit(ctx, out, ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: "stop"})
	}()
	return out, nil
}

func (p *weakProvider) ExecuteTasks(ctx context.Context, env *provider.AccountEnv, taskIDs []string) (*provider.CheckInReport, error) {
	sleep(ctx, 200*time.Millisecond)
	return &provider.CheckInReport{
		Success:      true,
		EarnedPoints: 50,
		Message:      "网页端签到成功 +50（模拟）",
		Results: []provider.TaskResult{
			{TaskID: "web_checkin", Title: "网页端签到（模拟）", Success: true, Points: 50},
		},
	}, nil
}

func (p *weakProvider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	return &provider.QuotaStatus{Total: 200, Remaining: 180, Unit: "次"}, nil
}

// --- 辅助 ---

// buildReply 根据请求内容生成一段可预测的回复。
//
// native=true 时模拟"原生工具调用"（返回纯文本 + 单独的 tool call 事件）；
// native=false 时模拟"弱渠道按注入的提示词输出 XML"（文本里直接带标签）。
func buildReply(req *ir.UnifiedChatRequest, native bool) (string, bool) {
	// 弱渠道的 req.Tools 已被 xml_tool_call 中间件清空，
	// 它只能从注入的提示词里知道"有工具可用"，这正是真实上游的处境。
	hasTools := len(req.Tools) > 0 || strings.Contains(req.Instructions, "<tool_call>")
	toolName := firstToolName(req)

	last := strings.TrimSpace(req.LastUserText())
	if last == "" {
		last = "（空）"
	}

	// 判断用户是否在要求读文件 —— 用一个简单触发词，方便手动测试。
	wantTool := hasTools && toolName != "" &&
		(strings.Contains(last, "读") || strings.Contains(last, "read") || strings.Contains(last, "文件"))

	var sb strings.Builder
	sb.WriteString("我是 Mock 渠道，收到你的请求：")
	if len([]rune(last)) > 60 {
		sb.WriteString(string([]rune(last)[:60]))
		sb.WriteString("…")
	} else {
		sb.WriteString(last)
	}

	if len(req.Items) > 0 {
		sb.WriteString(fmt.Sprintf("（上下文共 %d 项）", len(req.Items)))
	}
	if req.PreviousResponseID != "" {
		sb.WriteString("（已续接上一轮）")
	}

	if !native && wantTool {
		// 弱渠道：按注入的提示词输出 XML 块，由中间件截断并转为标准工具事件。
		sb.WriteString("\n\n我来读取该文件。\n<tool_call>\n")
		sb.WriteString(fmt.Sprintf(`{"name": %q, "arguments": {"path": "main.go"}}`, toolName))
		sb.WriteString("\n</tool_call>")
		return sb.String(), false
	}

	if native && wantTool {
		sb.WriteString("\n\n好的，我来读取这个文件。")
		return sb.String(), true
	}

	if req.ReasoningEffort != "" {
		sb.WriteString("\n\n（已按 reasoning.effort=" + req.ReasoningEffort + " 处理）")
	}
	return sb.String(), false
}

// firstToolName 从原生 Tools 或注入的提示词里取出第一个工具名。
func firstToolName(req *ir.UnifiedChatRequest) string {
	if len(req.Tools) > 0 {
		return req.Tools[0].Name
	}
	const marker = `<tool name="`
	idx := strings.Index(req.Instructions, marker)
	if idx < 0 {
		return ""
	}
	rest := req.Instructions[idx+len(marker):]
	if end := strings.Index(rest, `"`); end > 0 {
		return rest[:end]
	}
	return ""
}

// chunkString 按 rune 切分，避免把中文/emoji 切坏。
func chunkString(s string, size int) []string {
	r := []rune(s)
	if len(r) == 0 {
		return nil
	}
	var out []string
	for i := 0; i < len(r); i += size {
		end := i + size
		if end > len(r) {
			end = len(r)
		}
		out = append(out, string(r[i:end]))
	}
	return out
}

func emit(ctx context.Context, out chan<- ir.UnifiedStreamEvent, ev ir.UnifiedStreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

func estimateTokens(req *ir.UnifiedChatRequest) int {
	n := len([]rune(req.Instructions))
	for _, it := range req.Items {
		n += len([]rune(it.Text)) + len([]rune(it.Arguments)) + len([]rune(it.Output))
	}
	return n/3 + 1
}

func shortHash(s string) string {
	if s == "" {
		return "000000"
	}
	sum := 0
	for _, c := range s {
		sum = sum*31 + int(c)
		if sum < 0 {
			sum = -sum
		}
	}
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 8)
	for i := 0; i < 8; i++ {
		out[i] = hexdigits[sum%16]
		sum /= 16
		if sum == 0 {
			sum = 7
		}
	}
	return string(out)
}

func taskTitle(id string) string {
	switch id {
	case "daily_checkin":
		return "每日签到（模拟）"
	case "bonus_task":
		return "额外任务（模拟）"
	case "web_checkin":
		return "网页端签到（模拟）"
	default:
		return id
	}
}
