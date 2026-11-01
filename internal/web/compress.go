package web

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// 静态资源在局域网访问时走明文太浪费：CSS/JS/SVG 的压缩比通常在 3–5 倍。
// 只在客户端声明 gzip、且不是 Range / HEAD / 小文件时压缩；woff2 与图片本身已压缩，跳过。
var gzipPool = sync.Pool{New: func() any { return gzip.NewWriter(io.Discard) }}

type gzipResponseWriter struct {
	http.ResponseWriter
	req         *http.Request
	gz          *gzip.Writer
	decided     bool
	compressing bool
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.decided {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.decided = true
	h := w.Header()

	if code == http.StatusOK &&
		w.req.Method != http.MethodHead &&
		w.req.Header.Get("Range") == "" &&
		compressible(h.Get("Content-Type")) &&
		bigEnough(h.Get("Content-Length")) {
		w.compressing = true
		h.Del("Content-Length") // 压缩后长度未知，交给 chunked
		h.Set("Content-Encoding", "gzip")
		w.gz = gzipPool.Get().(*gzip.Writer)
		w.gz.Reset(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipResponseWriter) Write(p []byte) (int, error) {
	if !w.decided {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(p))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.compressing {
		return w.gz.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *gzipResponseWriter) Close() {
	if w.gz == nil {
		return
	}
	_ = w.gz.Close()
	gzipPool.Put(w.gz)
	w.gz = nil
}

func compressible(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
	switch ct {
	case "application/javascript", "application/json", "application/xml",
		"image/svg+xml", "application/manifest+json":
		return true
	}
	return strings.HasPrefix(ct, "text/")
}

func bigEnough(contentLength string) bool {
	if contentLength == "" {
		return true // 长度未知（chunked）时按可压缩处理
	}
	n, err := strconv.Atoi(contentLength)
	if err != nil {
		return true
	}
	return n >= 1024
}

func withCompression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w, req: r}
		defer gw.Close()
		next.ServeHTTP(gw, r)
	})
}
