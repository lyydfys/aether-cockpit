package web

import (
	"io/fs"
	"net/http"
	"sync"
)

// 四份 CSS 合并成一个 /css/app.css：没有构建链，但首屏少 3 个阻塞往返。
// 源文件仍按职责拆分，改样式直接改源文件；这里在首次请求时拼接并缓存。
var cssFiles = []string{
	"css/tokens.css",
	"css/base.css",
	"css/components.css",
	"css/views.css",
}

var (
	cssOnce  sync.Once
	cssBytes []byte
	cssErr   error
)

func bundledCSS(sub fs.FS) ([]byte, error) {
	cssOnce.Do(func() {
		var out []byte
		for _, name := range cssFiles {
			b, err := fs.ReadFile(sub, name)
			if err != nil {
				cssErr = err
				return
			}
			out = append(out, b...)
			out = append(out, '\n')
		}
		cssBytes = out
	})
	return cssBytes, cssErr
}

// withBundledCSS 在静态文件之前拦截 /css/app.css。
func withBundledCSS(sub fs.FS, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/css/app.css" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		b, err := bundledCSS(sub)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(b)
		}
	})
}
