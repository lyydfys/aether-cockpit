package gateway

import (
	"strings"

	"aether-cockpit/internal/core/ir"
)

// 把请求里的"组规则信号"抽出来。
//
// 这些条件（agents / folders / tokens / images）本质上是"请求长什么样"，
// 而路由层只该看到候选链与信号本身——所以解析放在这里，路由不碰协议。

// routeSignalsFor 从请求里收集组规则需要的信号。
//
// 取不到的一律留零值：规则里写了对应条件时"不成立"，比用错信号命中更安全。
func routeSignalsFor(req *ir.UnifiedChatRequest) RouteSignals {
	if req == nil {
		return RouteSignals{}
	}
	sig := RouteSignals{
		Tokens: len([]rune(req.LastUserText())),
		Images: requestHasImages(req),
		Agent:  metaString(req, "client_agent", "agent", "client"),
		Folder: metaString(req, "cwd", "working_dir", "folder", "project_dir"),
	}
	return sig
}

// requestHasImages 判断请求是否带图片（多模态附件）。
func requestHasImages(req *ir.UnifiedChatRequest) bool {
	for _, it := range req.Items {
		for _, a := range it.Attachments {
			if strings.EqualFold(a.Kind, "image") || strings.HasPrefix(strings.ToLower(a.MIME), "image/") {
				return true
			}
		}
	}
	return false
}

// metaString 依次尝试多个键，返回第一个非空字符串。
//
// Meta 的键由各入站解析器自行填写（不统一），所以这里做一次多键兜底。
func metaString(req *ir.UnifiedChatRequest, keys ...string) string {
	if req == nil || req.Meta == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := req.Meta[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}
