package gateway

import (
	"net/http"

	"aether-cockpit/internal/web"
)

// registerUI 挂载内嵌管理界面。
//
// 挂在根路径上：具体 API 路径（/v1/*、/api/*）已先注册，优先级更高，
// 只有未匹配的路径才会落到静态文件服务。
func (g *Gateway) registerUI(m *http.ServeMux) {
	// 重写中的自有前端：迁移期并行挂在 /next/，逐屏替换、随时可回退。
	// 根路径 = 自有前端（ui2）。旧 dist 不再内嵌/提供。
	m.Handle("/next/", http.StripPrefix("/next/", web.NextHandler()))
	m.Handle("/", web.Handler())
}
