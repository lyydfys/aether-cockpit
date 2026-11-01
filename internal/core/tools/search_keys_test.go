package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func tavilyKeyServer(t *testing.T, refusal map[string]int) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	used := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			APIKey string `json:"api_key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		used = append(used, body.APIKey)
		mu.Unlock()
		if code := refusal[body.APIKey]; code != 0 {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":"refused"}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"T","url":"https://t.dev","content":"C"}]}`))
	}))
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, used...)
	}
}

func TestSearchKeyRotation(t *testing.T) {
	srv, used := tavilyKeyServer(t, nil)
	defer srv.Close()
	cfg := SearchConfig{Provider: "tavily", Endpoint: srv.URL, APIKeys: []string{"k1", "k2"}}
	for i := 0; i < 3; i++ {
		if _, err := Search(context.Background(), cfg, "q", 1); err != nil {
			t.Fatal(err)
		}
	}
	got := strings.Join(used(), ",")
	if got != "k1,k2,k1" {
		t.Fatalf("rotate 顺序 = %s", got)
	}
}

func TestSearchKeyOrderAndCooldown(t *testing.T) {
	srv, used := tavilyKeyServer(t, map[string]int{"k1": http.StatusTooManyRequests})
	defer srv.Close()
	cfg := SearchConfig{Provider: "tavily", Endpoint: srv.URL, APIKeys: []string{"k1", "k2"}, KeyOrder: "order"}
	for i := 0; i < 2; i++ {
		res, err := Search(context.Background(), cfg, "q", 1)
		if err != nil || len(res) != 1 {
			t.Fatalf("run %d = %v %v", i, res, err)
		}
	}
	got := strings.Join(used(), ",")
	if got != "k1,k2,k2" {
		t.Fatalf("order+cooldown 顺序 = %s", got)
	}
}
