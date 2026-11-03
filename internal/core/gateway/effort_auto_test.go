package gateway

import (
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
)

func msgReq(texts ...string) *ir.UnifiedChatRequest {
	req := &ir.UnifiedChatRequest{Model: "m"}
	for _, t := range texts {
		req.Items = append(req.Items, ir.UnifiedItem{
			Type: ir.ItemTypeMessage, Role: "user", Text: t,
		})
	}
	return req
}

// 短问题给 low：闲聊不该按高价档跑推理。
func TestAutoEffortShortPromptIsLow(t *testing.T) {
	if got := AutoEffort(msgReq("你好，帮我把这个函数改成 async")); got != effortAutoLow {
		t.Fatalf("短问题应给 low，got %q", got)
	}
}

// 带代码块/堆栈 = 在改代码，给 high。
func TestAutoEffortCodeWorkIsHigh(t *testing.T) {
	code := "看这段：\n```go\nfunc main() { panic(\"x\") }\n```\n为什么会挂？"
	if got := AutoEffort(msgReq(code)); got != effortAutoHigh {
		t.Fatalf("代码块应给 high，got %q", got)
	}
	stack := "Traceback (most recent call last):\n  File \"a.py\", line 1"
	if got := AutoEffort(msgReq(stack)); got != effortAutoHigh {
		t.Fatalf("堆栈应给 high，got %q", got)
	}
	diff := "diff --git a/x.go b/x.go\n@@ -1,3 +1,4 @@"
	if got := AutoEffort(msgReq(diff)); got != effortAutoHigh {
		t.Fatalf("diff 应给 high，got %q", got)
	}
}

// 长上下文给 high：要读完再答。
func TestAutoEffortLongPromptIsHigh(t *testing.T) {
	long := strings.Repeat("要点说明。", autoEffortLongPrompt) // rune 数远超阈值
	if got := AutoEffort(msgReq(long)); got != effortAutoHigh {
		t.Fatalf("长上下文应给 high，got %q", got)
	}
}

// 工具多 = agent 多步规划，给 high。
func TestAutoEffortManyToolsIsHigh(t *testing.T) {
	req := msgReq("帮我查一下")
	for i := 0; i < autoEffortToolCount; i++ {
		req.Tools = append(req.Tools, ir.UnifiedToolSpec{Name: "t"})
	}
	if got := AutoEffort(req); got != effortAutoHigh {
		t.Fatalf("多工具应给 high，got %q", got)
	}
}

// 中间的普通问题给 medium；空请求不表态（保留渠道默认）。
func TestAutoEffortMediumAndEmpty(t *testing.T) {
	mid := strings.Repeat("这是一段普通长度的说明。", 20) // 240 rune：超过短问题阈值、远未到长上下文
	if got := AutoEffort(msgReq(mid)); got != effortAutoMedium {
		t.Fatalf("普通问题应给 medium，got %q（rune=%d）", got, len([]rune(mid)))
	}
	if got := AutoEffort(nil); got != "" {
		t.Fatalf("nil 请求应返回空，got %q", got)
	}
	if got := AutoEffort(&ir.UnifiedChatRequest{Model: "m"}); got != "" {
		t.Fatalf("空请求应返回空，got %q", got)
	}
}

// 系统提示不计入难度信号：否则所有带长 system prompt 的请求都会被判成 high。
func TestAutoEffortIgnoresSystemPrompt(t *testing.T) {
	req := msgReq("你好")
	req.Instructions = strings.Repeat("你是一个很有用的助手。", 2000)
	if got := AutoEffort(req); got != effortAutoLow {
		t.Fatalf("系统提示不该拉高档位，got %q", got)
	}
}
