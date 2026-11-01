// Package inbound 把三种下游协议解析成统一的 IR。
//
// 这里的代码是整个网关唯一"协议相关"的地方：新增一个下游协议只在这里加一个文件，
// 上游渠道插件完全不受影响。
package inbound

import (
	"encoding/json"
	"strings"

	"aether-cockpit/internal/core/ir"
)

// decodeJSONSafe 解析 JSON，错误信息带上下文便于定位是哪个客户端发了畸形包。
func decodeJSONSafe(body []byte, v any, what string) error {
	body = trimBOM(body)
	if len(body) == 0 {
		return ir.NewAPIError(ir.ErrCodeBadRequest, what+" 请求体为空")
	}
	if err := json.Unmarshal(body, v); err != nil {
		return ir.NewAPIErrorf(ir.ErrCodeBadRequest, "%s 请求体 JSON 解析失败: %v", what, err).WithCause(err)
	}
	return nil
}

func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

// joinInstructions 把多条 system 消息折叠成单一 Instructions 字符串。
//
// 折到 Instructions 而不是留在 Items 里，是为了让 Provider 侧只有一条明确的
// system 入口 —— 很多逆向渠道对"多条 system 消息"支持极差。
func joinInstructions(prev, add string) string {
	add = strings.TrimSpace(add)
	if add == "" {
		return prev
	}
	if prev == "" {
		return add
	}
	return prev + "\n\n" + add
}

// decodeStringOrArray 处理 "stop" 这类既可以是字符串也可以是字符串数组的字段。
func decodeStringOrArray(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	return nil
}

// setClientSession 记录客户端自报的会话标识（OpenAI 的 user / Anthropic 的
// metadata.user_id）。它是会话亲和的第一级来源：只要客户端稳定发送，
// 比"对首尾文本做哈希"更准确，也不受历史被裁剪的影响。
func setClientSession(req *ir.UnifiedChatRequest, sid string) {
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return
	}
	if req.Meta == nil {
		req.Meta = make(map[string]any, 2)
	}
	req.Meta["client_session"] = sid
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
