package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetchBlocksLinkLocal(t *testing.T) {
	for _, u := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://[fe80::1]/",
		"http://100.100.100.200/latest/meta-data/",
	} {
		if _, err := fetchPageText(context.Background(), u, 100); err == nil || !strings.Contains(err.Error(), "元数据") {
			t.Fatalf("%s 未被拦截: %v", u, err)
		}
	}
}

func TestWebFetchBlocksRedirectToMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()
	if _, err := fetchPageText(context.Background(), srv.URL, 100); err == nil || !strings.Contains(err.Error(), "元数据") {
		t.Fatalf("重定向未被拦截: %v", err)
	}
}

func TestWebFetchAllowsLocalhost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<p>ok</p>"))
	}))
	defer srv.Close()
	text, err := fetchPageText(context.Background(), srv.URL, 100)
	if err != nil || !strings.Contains(text, "ok") {
		t.Fatalf("本地页面 = %q %v", text, err)
	}
}
