package gateway

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// mobileAdminPath 返回该路径是否属于移动管理面。
//
// 模型协议（/v1、/v1beta、/responses/compact）使用独立的 caller key，
// 不在管理会话里；静态 UI 与图标不携带数据。/healthz 是运行时探针，
// 由 internal/mobile 在进入 Gateway 前处理。
func (g *Gateway) mobileAdminPath(path string) bool {
	switch {
	case path == "/api/health":
		return false
	case path == "/api" || strings.HasPrefix(path, "/api/"):
		return true
	case path == "/mcp" || strings.HasPrefix(path, "/mcp/"):
		return true
	case path == "/metrics", path == "/readyz", path == "/version":
		return true
	}
	return false
}

// mobileAdminAllowed 只接受 Authorization: Bearer <AdminSessionKey>。
// 刻意不读 query：凭据不得出现在 URL、代理日志或历史记录里。
func (g *Gateway) mobileAdminAllowed(r *http.Request) bool {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(token), []byte(g.cfg.AdminSessionKey)) == 1
}

// mobileCallerAllowed 校验模型侧 caller key。接受 OpenAI 风格的
// Authorization: Bearer 与 Anthropic/Gemini 风格的 x-api-key，
// 但不读取 query（凭据不得进 URL）。未配置 key 时失败关闭。
func (g *Gateway) mobileCallerAllowed(r *http.Request) bool {
	if g.cfg.CallerKey == "" {
		return false
	}
	token := ""
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	const prefix = "Bearer "
	if len(header) > len(prefix) && strings.EqualFold(header[:len(prefix)], prefix) {
		token = strings.TrimSpace(header[len(prefix):])
	} else if v := strings.TrimSpace(r.Header.Get("x-api-key")); v != "" {
		token = v
	}
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(g.cfg.CallerKey)) == 1
}
