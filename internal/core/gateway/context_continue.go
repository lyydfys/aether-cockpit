package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"aether-cockpit/internal/provider"
)

// 上下文自动续接：上游以"上下文超限"拒绝时，不再直接把错误抛给客户端，
// 而是裁掉较早的对话（保留 instructions 与最近若干条）后重试一次。
//
// 为什么不用 /responses/compact：那个端点要求客户端主动调用并接收
// compaction 载荷；这里是"客户端只想要一条正常回复"的场景，只能用
// 本地裁剪让请求重新装进窗口。裁剪点尽量落在 user 消息边界，
// 避免把 tool_call 与 tool_result 拆散。

// contextKeepTailItems 是裁剪后保留的最近条目数。
const contextKeepTailItems = 40

// contextOverflowHints 是各上游对"上下文超限"的常见措辞（小写匹配）。
var contextOverflowHints = []string{
	"context_length",
	"context length",
	"context window",
	"maximum context",
	"too many tokens",
	"input exceeds",
	"exceeds the context",
	"prompt is too long",
	"token limit",
}

// isContextOverflow 判断上游错误是否是"上下文超限"。
func isContextOverflow(status int, body []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusRequestEntityTooLarge {
		return false
	}
	text := strings.ToLower(string(body))
	for _, hint := range contextOverflowHints {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}

// shrinkResponsesInput 裁掉较早的 input 条目，保留最近 keepTail 条；
// 裁剪点会前进到下一个 user 消息，避免拆散工具调用对。
// 返回新请求体、丢弃的条目数与是否成功。
func shrinkResponsesInput(raw []byte, keepTail int) ([]byte, int, bool) {
	if keepTail <= 0 {
		return nil, 0, false
	}
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil, 0, false
	}
	items, ok := root["input"].([]any)
	if !ok || len(items) <= keepTail {
		return nil, 0, false
	}
	cut := len(items) - keepTail
	for cut < len(items) {
		if item, ok := items[cut].(map[string]any); ok {
			if role, _ := item["role"].(string); role == "user" {
				break
			}
		}
		cut++
	}
	if cut >= len(items) {
		cut = len(items) - keepTail
	}
	dropped := cut
	root["input"] = append([]any(nil), items[cut:]...)
	out, err := json.Marshal(root)
	if err != nil {
		return nil, 0, false
	}
	return out, dropped, true
}

// countResponseItems 统计 input 条目数（用于回填审计日志）。
func countResponseItems(raw []byte) int {
	var root struct {
		Input []json.RawMessage `json:"input"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return 0
	}
	return len(root.Input)
}

// tryContextContinue 在 4xx 且错误说明上下文超限时裁剪并重试一次。
// 无论成功与否都会关闭传入的 resp。成功时返回重试响应与新请求体。
func tryContextContinue(ctx context.Context, native provider.NativeResponsesCapability, env *provider.AccountEnv,
	body []byte, upstreamModel string, resp *http.Response) (*http.Response, []byte, int, bool) {
	errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if !isContextOverflow(resp.StatusCode, errBody) {
		return nil, body, 0, false
	}
	shrunk, dropped, ok := shrinkResponsesInput(body, contextKeepTailItems)
	if !ok {
		return nil, body, 0, false
	}
	retry, err := native.ResponsesPassthrough(ctx, env, shrunk, upstreamModel)
	if err != nil {
		return nil, body, 0, false
	}
	if retry.StatusCode < 200 || retry.StatusCode >= 300 {
		_ = retry.Body.Close()
		return nil, body, 0, false
	}
	return retry, shrunk, dropped, true
}
