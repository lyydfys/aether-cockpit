package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// 客户端在排队时断开：等待计数必须回落到 0，不能泄漏。
func TestQueueCancelReleasesWaiter(t *testing.T) {
	g, env, srv := queueGateway(t, 3*time.Second)
	release, _ := env.TryAcquire(1)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/chat/completions",
			strings.NewReader(`{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"cancel-me"}]}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	// 等它进入排队。
	deadline := time.Now().Add(time.Second)
	for g.laneWaiting("mock-native") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if g.laneWaiting("mock-native") == 0 {
		t.Fatal("请求应已进入排队")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("取消后请求未返回")
	}
	// 等待计数应回落（取消路径会减一）。
	deadline = time.Now().Add(time.Second)
	for g.laneWaiting("mock-native") != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := g.laneWaiting("mock-native"); got != 0 {
		t.Fatalf("取消后 waiting 应回 0，得到 %d", got)
	}
}
