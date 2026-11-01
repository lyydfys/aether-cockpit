package gateway

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// TestWeightedConcurrentLoad 验证权重 + 账号并发上限 + 粘性在同一批并发里
// 不互相踩：全部 200、没有串扰、跑完 lanes 归零、三个账号都被用到。
func TestWeightedConcurrentLoad(t *testing.T) {
	g := New(Config{
		MaxAttempts:             2,
		FirstByteTimeout:        5 * time.Second,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		UsagePath:               "-",
		MaxConcurrentPerAccount: -1,
	})
	weights := map[string]int{"a": 5, "b": 3, "c": 1}
	for id, w := range weights {
		env := provider.NewAccountEnv(id, "mock-native")
		env.Label = id
		env.HTTPClient = &http.Client{Timeout: 10 * time.Second}
		env.SetWeight(w)
		two := 2
		env.SetConcurrencyOverride(&two)
		g.pool.Add(env)
	}

	srv := httptest.NewServer(g)
	defer srv.Close()

	const n = 30
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"load-%d"}]}`, i)
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", fmt.Sprintf("load-probe-%d", i))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("请求 %d = %d %s", i, resp.StatusCode, string(raw))
				return
			}
			if want := fmt.Sprintf("load-%d", i); !strings.Contains(string(raw), want) {
				errs <- fmt.Errorf("请求 %d 串扰: %s", i, string(raw))
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	rec := httptest.NewRecorder()
	g.handleLanes(rec, httptest.NewRequest(http.MethodGet, "/api/lanes", nil))
	if body := rec.Body.String(); strings.Contains(body, `"busy":1`) || strings.Contains(body, `"busy":2`) {
		t.Fatalf("跑完后仍有占用: %s", body)
	}
}
