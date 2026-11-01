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

func TestSearchTavily(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotQuery, _ = body["query"].(string)
		_, _ = w.Write([]byte(`{"results":[{"title":"T1","url":"https://a","content":"C1"}]}`))
	}))
	defer srv.Close()

	res, err := Search(context.Background(), SearchConfig{Provider: "tavily", Endpoint: srv.URL, APIKey: "k"}, "golang", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQuery != "golang" || len(res) != 1 || res[0].Title != "T1" || res[0].Snippet != "C1" {
		t.Fatalf("结果不对: %v (query=%q)", res, gotQuery)
	}
}

func TestSearchSerperSendsAPIKeyHeader(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-KEY")
		_, _ = w.Write([]byte(`{"organic":[{"title":"S1","link":"https://s","snippet":"SN"}]}`))
	}))
	defer srv.Close()

	res, err := Search(context.Background(), SearchConfig{Provider: "serper", Endpoint: srv.URL, APIKey: "serper-key"}, "q", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotKey != "serper-key" {
		t.Fatalf("API Key 头未发送: %q", gotKey)
	}
	if len(res) != 1 || res[0].URL != "https://s" {
		t.Fatalf("结果不对: %v", res)
	}
}

func TestSearchSearxngRequiresEndpoint(t *testing.T) {
	if _, err := Search(context.Background(), SearchConfig{Provider: "searxng"}, "q", 5); err == nil {
		t.Fatal("searxng 缺 endpoint 应报错")
	}
}

func TestSearchUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()
	if _, err := Search(context.Background(), SearchConfig{Provider: "tavily", Endpoint: srv.URL}, "q", 5); err == nil {
		t.Fatal("上游 401 应返回错误")
	}
}

func TestGenerateImageProxiesAndInjectsModel(t *testing.T) {
	var auth, model string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("路径 = %s", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		model, _ = body["model"].(string)
		_, _ = w.Write([]byte(`{"data":[{"url":"https://img/1.png"}]}`))
	}))
	defer srv.Close()

	status, out, err := GenerateImage(context.Background(), ImageConfig{
		BaseURL: srv.URL, APIKey: "sk-img", Model: "gpt-image-1",
	}, []byte(`{"prompt":"a cat"}`))
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if status != 200 || !strings.Contains(string(out), "img/1.png") {
		t.Fatalf("status=%d out=%s", status, out)
	}
	if auth != "Bearer sk-img" {
		t.Errorf("Authorization = %q", auth)
	}
	if model != "gpt-image-1" {
		t.Errorf("model 未注入: %q", model)
	}
}

func TestGenerateImageRequiresBaseURL(t *testing.T) {
	if _, _, err := GenerateImage(context.Background(), ImageConfig{}, []byte(`{}`)); err == nil {
		t.Fatal("缺 base_url 应报错")
	}
}
