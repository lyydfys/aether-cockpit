// Package web 内嵌单页管理界面。
//
// 选择内嵌静态资源而不是 Wails/Electron：
//   - 自用场景不需要系统托盘与原生窗口，一个浏览器标签足够；
//   - 省掉整条 Node 构建链与桌面打包流程，40% 工作量直接砍掉；
//   - 内核完全独立于 UI，将来要套 Wails 时只需换这一层。
package web

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// 字体 MIME 写死，不再依赖 Windows 注册表：.woff2 若落到 application/octet-stream，
// 配合上面的 nosniff 会被浏览器直接拒掉，界面会静默回落成系统字体。
func init() {
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".woff", "font/woff")
}

//go:embed ui2
var embedded embed.FS

// FS 返回自有前端（ui2）的文件系统；桌面端也用它。
func FS() (fs.FS, error) {
	return fs.Sub(embedded, "ui2")
}

// Handler 返回静态资源处理器：现在指向自有前端 ui2。
func Handler() http.Handler { return handler() }

// NextHandler 提供重写中的自有前端（ui2），挂在 /next/ 下。
func NextHandler() http.Handler { return handler() }

func handler() http.Handler {
	sub, err := fs.Sub(embedded, "ui2")
	if err != nil {
		return http.NotFoundHandler()
	}
	return withCompression(withSecurityHeaders(withBundledCSS(sub, noDirListing(sub))))
}

// noDirListing 让目录路径直接 404，而不是把文件清单列给浏览器；
// 根路径仍交给 FileServer 去取 index.html。
func noDirListing(sub fs.FS) http.Handler {
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.URL.Path)
		if p != "/" {
			st, err := fs.Stat(sub, strings.TrimPrefix(p, "/"))
			if err != nil || st.IsDir() {
				http.NotFound(w, r)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		// 前端资源随 exe 打包，本地没有 CDN；禁掉一切缓存，
		// 否则 WebView2/浏览器会把旧 CSS/JS 留在磁盘里，改版后看到的还是旧界面。
		h.Set("Cache-Control", "no-store")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// 界面没有内联脚本；样式属性由前端构造器写入，所以 style-src 允许 inline。
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self'; connect-src 'self'; font-src 'self' data:; object-src 'none'; "+
				"base-uri 'none'; frame-ancestors 'none'")
		// 文件名没有内容哈希，宁可每次协商也不要发版后拿到旧脚本。
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}
