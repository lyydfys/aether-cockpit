package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesOwnUIIWithSecurityHeaders(t *testing.T) {
	h := Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<div id="app"`) || !strings.Contains(body, "js/app.js") {
		t.Fatalf("root html is not the own ui2 shell: %q", body[:min(120, len(body))])
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP missing frame-ancestors: %q", csp)
	}
}

func TestHandlerHidesDirectoryListings(t *testing.T) {
	h := Handler()
	for _, p := range []string{"/css/", "/js/", "/js/views/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404 (directory listing)", p, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/js/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /js/app.js = %d, want 200", rec.Code)
	}
}

func TestCompressionOnlyForCompressibleAssets(t *testing.T) {
	h := Handler()

	read := func(t *testing.T, path string, accept string) (*http.Response, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if accept != "" {
			req.Header.Set("Accept-Encoding", accept)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		res := rec.Result()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if res.Header.Get("Content-Encoding") == "gzip" {
			zr, err := gzip.NewReader(strings.NewReader(string(body)))
			if err != nil {
				t.Fatalf("gzip reader %s: %v", path, err)
			}
			plain, err := io.ReadAll(zr)
			if err != nil {
				t.Fatalf("gunzip %s: %v", path, err)
			}
			body = plain
		}
		return res, string(body)
	}

	res, body := read(t, "/css/tokens.css", "gzip, deflate")
	if res.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("css should be gzipped, got %q", res.Header.Get("Content-Encoding"))
	}
	if res.Header.Get("Content-Length") != "" {
		t.Fatalf("compressed response must not keep Content-Length")
	}
	if !strings.Contains(body, "--fs-m") {
		t.Fatalf("gunzipped css looks wrong: %q", body[:min(60, len(body))])
	}

	res, _ = read(t, "/css/tokens.css", "")
	if res.Header.Get("Content-Encoding") == "gzip" {
		t.Fatalf("no Accept-Encoding should not compress")
	}

	res, _ = read(t, "/assets/fonts/inter-latin.woff2", "gzip")
	if res.Header.Get("Content-Encoding") == "gzip" {
		t.Fatalf("woff2 is already compressed and must not be gzipped")
	}
	if res.Header.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("Vary = %q, want Accept-Encoding", res.Header.Get("Vary"))
	}
}

// 中文字体是子集产物（scripts/build-font.py），漏提交或没重跑时不能静默回落成系统字体。
func TestCJKFontsEmbedded(t *testing.T) {
	h := Handler()
	for _, w := range []string{"400", "500", "600"} {
		name := "/assets/fonts/noto-sans-sc-" + w + ".woff2"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, name, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d（是否忘了运行 scripts/build-font.py？）", name, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "font/woff2") {
			t.Fatalf("%s content-type = %q, want font/woff2", name, ct)
		}
		body := rec.Body.Bytes()
		if !bytes.HasPrefix(body, []byte("wOF2")) {
			t.Fatalf("%s 不是 woff2（magic 不对）", name)
		}
		if len(body) < 200_000 {
			t.Fatalf("%s 只有 %d 字节，子集可能不完整", name, len(body))
		}
	}
}

func TestBundledCSS(t *testing.T) {
	h := Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/css/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /css/app.css = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, marker := range []string{"--fs-m", "box-sizing", ".acct-card", ".settings-layout"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("bundled css missing %q", marker)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Fatalf("content-type = %q", ct)
	}
}
