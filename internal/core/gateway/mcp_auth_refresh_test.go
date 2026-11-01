package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMCPBearerRefreshSingleFlight 并发到期请求只续期一次（轮换 refresh_token 时
// 并发续期会互相作废）；网络错误不标记 dead，明确拒绝才标记。
func TestMCPBearerRefreshSingleFlight(t *testing.T) {
	var calls int32
	var mode atomic.Value
	mode.Store("ok")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		switch mode.Load().(string) {
		case "500":
			http.Error(w, "upstream down", http.StatusInternalServerError)
			return
		case "400":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-new", "refresh_token": "rt-new", "token_type": "Bearer", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	newGW := func() *Gateway {
		g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
		store := newMCPAuthStore(filepath.Join(t.TempDir(), "mcp-auth.json"))
		_ = store.put(&mcpAuthRecord{
			Name: "demo", Upstream: "https://up.example/mcp",
			Token:  oauthToken{AccessToken: "at-old", RefreshToken: "rt-old", ExpiresAt: time.Now().Add(-time.Minute).Unix()},
			Client: oauthClient{ClientID: "c1"},
			Meta:   oauthASMeta{TokenEndpoint: srv.URL},
		})
		g.mcpAuth = store
		return g
	}

	g := newGW()
	var wg sync.WaitGroup
	okCount := int32(0)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tok, ok := g.mcpBearerFor("demo", "https://up.example/mcp"); ok && tok == "at-new" {
				atomic.AddInt32(&okCount, 1)
			}
		}()
	}
	wg.Wait()
	if okCount != 8 {
		t.Fatalf("ok = %d/8", okCount)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("并发续期次数 = %d，应为 1", got)
	}
	if rec := g.mcpAuthStore().get("demo"); rec.Token.RefreshToken != "rt-new" {
		t.Fatalf("轮换后的 refresh_token 未保存: %+v", rec.Token)
	}

	// 网络错误：本次失败但不标记失效。
	g = newGW()
	mode.Store("500")
	if _, ok := g.mcpBearerFor("demo", "https://up.example/mcp"); ok {
		t.Fatal("500 时应返回 false")
	}
	if rec := g.mcpAuthStore().get("demo"); rec.Dead {
		t.Fatal("网络错误不应把登录标记为失效")
	}
	mode.Store("ok")
	if tok, ok := g.mcpBearerFor("demo", "https://up.example/mcp"); !ok || tok != "at-new" {
		t.Fatalf("网络恢复后应能续期: %q %v", tok, ok)
	}

	// 明确拒绝：标记失效，需要重新登录。
	g = newGW()
	mode.Store("400")
	if _, ok := g.mcpBearerFor("demo", "https://up.example/mcp"); ok {
		t.Fatal("400 时应返回 false")
	}
	if rec := g.mcpAuthStore().get("demo"); !rec.Dead {
		t.Fatal("授权服务器拒绝后应标记失效")
	}
}
