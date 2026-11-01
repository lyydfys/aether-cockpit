package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchBrave(t *testing.T) {
	var gotPath, gotKey, gotQ string
	var gotCount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey, gotQ, gotCount = r.URL.Path, r.Header.Get("X-Subscription-Token"), r.URL.Query().Get("q"), r.URL.Query().Get("count")
		_, _ = w.Write([]byte(`{"web":{"results":[{"title":"<b>Go</b>","url":"https://go.dev","description":"the <strong>language</strong>"}]}}`))
	}))
	defer srv.Close()
	res, err := Search(context.Background(), SearchConfig{Provider: "brave", Endpoint: srv.URL, APIKey: "bk"}, "golang", 3)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/res/v1/web/search" || gotKey != "bk" || gotQ != "golang" || gotCount != "3" {
		t.Fatalf("request = %s key=%q q=%q count=%q", gotPath, gotKey, gotQ, gotCount)
	}
	if len(res) != 1 || res[0].Title != "Go" || res[0].Snippet != "the language" {
		t.Fatalf("res = %+v", res)
	}
}

func TestSearchExa(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.Header.Get("X-API-Key") != "ek" {
			t.Errorf("path=%s key=%q", r.URL.Path, r.Header.Get("X-API-Key"))
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"results":[{"title":"E","url":"https://e.dev","text":"ET"}]}`))
	}))
	defer srv.Close()
	res, err := Search(context.Background(), SearchConfig{Provider: "exa", Endpoint: srv.URL, APIKey: "ek"}, "q", 4)
	if err != nil {
		t.Fatal(err)
	}
	if body["numResults"] != float64(4) || body["query"] != "q" {
		t.Fatalf("body = %v", body)
	}
	if len(res) != 1 || res[0].Snippet != "ET" {
		t.Fatalf("res = %+v", res)
	}
}

func TestSearchFirecrawlV2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/search" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"data":{"web":[{"title":"F","url":"https://f.dev","description":"FD"}]}}`))
	}))
	defer srv.Close()
	res, err := Search(context.Background(), SearchConfig{Provider: "firecrawl", Endpoint: srv.URL, APIKey: "fk"}, "q", 2)
	if err != nil || len(res) != 1 || res[0].Snippet != "FD" {
		t.Fatalf("res = %+v err=%v", res, err)
	}
}

func TestSearchDeepseekTruncatesQuery(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"results":[{"title":"D","url":"https://d.dev","summary":"DS"}]}`))
	}))
	defer srv.Close()
	long := strings.Repeat("x", 200)
	res, err := Search(context.Background(), SearchConfig{Provider: "deepseek", Endpoint: srv.URL, APIKey: "dk"}, long, 2)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := body["query"].(string)
	if len([]rune(q)) != 128 {
		t.Fatalf("query 长度 = %d", len([]rune(q)))
	}
	if len(res) != 1 || res[0].Snippet != "DS" {
		t.Fatalf("res = %+v", res)
	}
}

func TestSearchYouKeyedAndFree(t *testing.T) {
	var method, path, key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, key = r.Method, r.URL.Path, r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(`{"results":{"web":[{"title":"Y","url":"https://y.dev","snippets":["one","two"]}],"news":[]}}`))
	}))
	defer srv.Close()

	res, err := Search(context.Background(), SearchConfig{Provider: "you", Endpoint: srv.URL, APIKey: "yk"}, "q", 3)
	if err != nil || method != "POST" || path != "/v1/search" || key != "yk" {
		t.Fatalf("keyed = %s %s key=%q err=%v", method, path, key, err)
	}
	if len(res) != 1 || res[0].Snippet != "one two" {
		t.Fatalf("res = %+v", res)
	}

	res, err = Search(context.Background(), SearchConfig{Provider: "you", Endpoint: srv.URL}, "q", 3)
	if err != nil || method != "GET" || path != "/v1/agents/search" || key != "" {
		t.Fatalf("free = %s %s key=%q err=%v", method, path, key, err)
	}
	if len(res) != 1 || res[0].Title != "Y" {
		t.Fatalf("free res = %+v", res)
	}
}
