package rtstats

import "strings"

// recordFromPrefix 从响应体的头部片段里提取响应 id 并记录。
//
// 这里只做"找第一处 \"id\": 取值"，不解析 JSON：流式 SSE 的每一帧是独立的
// data: 行，整体并不是合法 JSON；而且我们只要 id 这一个字段。
func recordFromPrefix(providerID string, prefix []byte) {
	if id := extractID(string(prefix)); id != "" {
		Record(providerID, id)
	}
}

// extractID 返回头部片段里第一个 "id" 字段的字符串值；找不到返回空。
func extractID(s string) string {
	for {
		i := strings.Index(s, `"id"`)
		if i < 0 {
			return ""
		}
		rest := s[i+4:]
		// 跳过 "id" 与值之间的 : " 等字符。
		rest = strings.TrimLeft(rest, " \t\r\n:")
		if !strings.HasPrefix(rest, `"`) {
			// 不是字符串值（可能是对象/数字），换下一个 "id" 继续找。
			s = rest
			continue
		}
		rest = rest[1:]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			return ""
		}
		id := rest[:end]
		// 过滤掉明显不是响应 id 的字段（例如 '"id":""'）。
		if id == "" {
			s = rest
			continue
		}
		return id
	}
}
