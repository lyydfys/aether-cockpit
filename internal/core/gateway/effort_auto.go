package gateway

import (
	"strings"

	"aether-cockpit/internal/core/ir"
)

// 自适应 reasoning effort：请求没显式指定档位时，按请求本身的"难度信号"
// 选一个合理档，而不是一律用渠道默认。
//
// 为什么用启发式而不是分类模型：
//   - 逐请求调一次分类模型要额外付费、额外延迟，而 effort 本身就是在省钱；
//   - 启发式可预测、可解释、可单测，用户看到"为什么这次是 high"能对上；
//   - 真正需要"选模型 + 选档"的场景已经有 smart 意图路由在做，
//     这里只解决"同一个模型该用多大劲"这一层。
//
// 输出只有 low/medium/high 三档：更细的档位（xhigh/max）留给用户显式指定，
// 自动判断给太高的档是浪费，给太低会掉质量。
const (
	effortAutoLow    = "low"
	effortAutoMedium = "medium"
	effortAutoHigh   = "high"

	// autoEffortShortPrompt 以下是"闲聊/确认"级：短问题不需要长思考。
	autoEffortShortPrompt = 160
	// autoEffortLongPrompt 以上是"贴了大段上下文"：需要仔细读，给 high。
	autoEffortLongPrompt = 6000
	// autoEffortToolCount 是工具数量阈值：工具多说明是 agent 场景，
	// 多步规划更吃推理。
	autoEffortToolCount = 4
)

// AutoEffort 按请求内容给一个启发式档位。
//
// 判定顺序（先命中先返回）：
//  1. 带代码块 / 报错堆栈 / 大段 diff → high（改代码是最需要思考的场景）；
//  2. prompt 很长 → high（大上下文要读完再答）；
//  3. 带多个工具 → high（agent 多步规划）；
//  4. 很短的问题 → low（省一半以上推理 token）；
//  5. 其余 → medium。
//
// 空请求返回空串（由调用方保留渠道默认）。
func AutoEffort(req *ir.UnifiedChatRequest) string {
	if req == nil {
		return ""
	}
	text, tools := effortSignals(req)
	if text == "" && tools == 0 {
		return ""
	}
	low := strings.ToLower(text)

	if looksLikeCodeWork(low) {
		return effortAutoHigh
	}
	if len([]rune(text)) >= autoEffortLongPrompt {
		return effortAutoHigh
	}
	if tools >= autoEffortToolCount {
		return effortAutoHigh
	}
	if len([]rune(text)) <= autoEffortShortPrompt && tools == 0 {
		return effortAutoLow
	}
	return effortAutoMedium
}

// effortSignals 汇总请求的"难度信号"：正文总长度与工具数量。
//
// 只看用户/助手文本，不看系统提示——系统提示通常很长且固定，
// 算进去会让所有请求都变成 high，等于没判定。
func effortSignals(req *ir.UnifiedChatRequest) (string, int) {
	var b strings.Builder
	for i := range req.Items {
		it := &req.Items[i]
		if it.Type == ir.ItemTypeMessage && (it.Role == "user" || it.Role == "assistant") {
			if it.Text != "" {
				b.Grow(len(it.Text) + 2)
				b.WriteString(it.Text)
				b.WriteString("\n")
			}
		}
	}
	return b.String(), len(req.Tools)
}

// looksLikeCodeWork 判断文本里是否有"在改代码"的信号。
//
// 用最小的特征集合，宁可漏判也不要误判成 high：误判成 high 会让闲聊也走
// 昂贵推理，用户能直接感知到变慢变贵。
func looksLikeCodeWork(lower string) bool {
	if strings.Contains(lower, "```") {
		return true
	}
	if strings.Contains(lower, "diff --git") || strings.Contains(lower, "@@ -") {
		return true
	}
	for _, sig := range []string{
		"panic:", "traceback (most recent call last)", "stack trace",
		"exception in thread", "segmentation fault", "npm err!",
		"error ts", "syntaxerror", "typeerror", "nullpointerexception",
	} {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

// autoEffortFor 把启发式结果收窄到模型支持的档位。
//
// 模型没有档位目录（Efforts 为空）时原样返回：这类模型通常只有"推理开/关"，
// 传 low/high 也不会被识别，收窄与否没差别，不如保留语义让下游自己决定。
func (g *Gateway) autoEffortFor(req *ir.UnifiedChatRequest) string {
	want := AutoEffort(req)
	if want == "" {
		return ""
	}
	return g.clampReasoningEffort(req.Model, want)
}
