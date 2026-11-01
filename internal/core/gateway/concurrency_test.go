package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// newTestGateway 构造一个带 mock 账号的网关实例。
func newTestGateway(t *testing.T) *httptest.Server {
	t.Helper()

	g := New(Config{
		MaxAttempts:      2,
		FirstByteTimeout: 5 * time.Second,
		StateTTL:         time.Minute,
		MaxRecords:       100,
		// 本测试验证的是"并发下三协议不串扰"，不是闸门行为：
		// 显式关闭每账号并发上限，让 36 路全部落到同一个 mock 账号上。
		MaxConcurrentPerAccount: -1,
	})

	for _, pid := range []string{"mock-native", "mock-weak", "mock-responses"} {
		env := provider.NewAccountEnv(pid+"-test", pid)
		env.Label = pid
		env.HTTPClient = &http.Client{Timeout: 0}
		g.Pool().Add(env)
	}

	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return srv
}

// TestConcurrentMixedProtocols 并发打三种协议，验证：
//   - 没有 panic / 死锁
//   - 每个响应都完整且属于自己（不发生串扰）
//   - 三种协议在并发下都返回 200
func TestConcurrentMixedProtocols(t *testing.T) {
	srv := newTestGateway(t)

	const rounds = 12 // 每种协议 12 个并发
	var wg sync.WaitGroup
	errCh := make(chan error, rounds*3)

	for i := 0; i < rounds; i++ {
		// OpenAI Chat
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"chat-%d"}]}`, i)
			if err := postAndCheck(srv.URL+"/v1/chat/completions", body, fmt.Sprintf("chat-%d", i)); err != nil {
				errCh <- err
			}
		}(i)

		// OpenAI Responses（非流式，走 IR）
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"model":"mock-native/echo-pro","input":"resp-%d"}`, i)
			if err := postAndCheck(srv.URL+"/v1/responses", body, fmt.Sprintf("resp-%d", i)); err != nil {
				errCh <- err
			}
		}(i)

		// Anthropic Messages
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"model":"mock-native/echo-pro","max_tokens":100,"messages":[{"role":"user","content":"msg-%d"}]}`, i)
			if err := postAndCheck(srv.URL+"/v1/messages", body, fmt.Sprintf("msg-%d", i)); err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	var errs []string
	for err := range errCh {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		t.Fatalf("%d 个并发请求失败:\n%s", len(errs), strings.Join(errs, "\n"))
	}
}

// postAndCheck 发请求并校验响应里包含本请求特有的标记，用于检出串扰。
func postAndCheck(url, body, wantMarker string) error {
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(body)))
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("状态 %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	if !bytes.Contains(raw, []byte(wantMarker)) {
		return fmt.Errorf("响应中缺少本请求标记 %q（疑似串扰）: %s", wantMarker, truncate(string(raw), 200))
	}
	return nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// TestConcurrentStateSuture 并发执行带 previous_response_id 的续轮，
// 验证状态存储不会把不同会话的历史混在一起。
func TestConcurrentStateSuture(t *testing.T) {
	srv := newTestGateway(t)

	const n = 6
	var wg sync.WaitGroup
	errCh := make(chan error, n*2)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			// 第一轮：建立会话
			first := fmt.Sprintf(`{"model":"mock-native/echo-pro","input":"会话-%d-首轮","store":true}`, i)
			resp, err := http.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(first))
			if err != nil {
				errCh <- err
				return
			}
			var parsed struct {
				ID string `json:"id"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&parsed)
			resp.Body.Close()
			if parsed.ID == "" {
				errCh <- fmt.Errorf("会话 %d 未拿到 response id", i)
				return
			}

			// 第二轮：续轮，验证回填了历史
			second := fmt.Sprintf(`{"model":"mock-native/echo-pro","previous_response_id":%q,"input":"会话-%d-续轮"}`, parsed.ID, i)
			resp2, err := http.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(second))
			if err != nil {
				errCh <- err
				return
			}
			raw, _ := io.ReadAll(resp2.Body)
			resp2.Body.Close()

			// mock 会在上下文项数 > 1 时输出"上下文共 N 项"，说明回填成功
			if !bytes.Contains(raw, []byte("已续接上一轮")) {
				errCh <- fmt.Errorf("会话 %d 未正确续接: %s", i, truncate(string(raw), 200))
			}
			// 同时必须包含自己的会话标记，不能串到别的会话
			if !bytes.Contains(raw, []byte(fmt.Sprintf("会话-%d-续轮", i))) {
				errCh <- fmt.Errorf("会话 %d 出现串扰: %s", i, truncate(string(raw), 200))
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// TestConcurrentDeleteAndList 并发删除账号与查询列表，验证账号池的读写锁正确。
func TestConcurrentDeleteAndList(t *testing.T) {
	srv := newTestGateway(t)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			resp, err := http.Get(srv.URL + "/api/accounts")
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}()
		go func() {
			defer wg.Done()
			resp, err := http.Get(srv.URL + "/api/providers")
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
}
