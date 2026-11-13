package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

// 首字节超时关闭后，慢上游不能被判死。
//
// 背景：中继站的首字节有时要一两分钟（模型排队、跨线路上游），
// 固定 60 秒判死会让"慢但能用"的线路永远用不上。
// 这里让上游 3 秒才吐首字节，并把超时设成 0（不限），必须成功。
func TestZeroFirstByteTimeoutMeansUnlimited(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second) // 慢于一个很短的超时，但远小于测试超时
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
		MaxAttempts:      1,
		FirstByteTimeout: 60 * time.Second,
	})
	defer func() { _ = g.Close(t.Context()) }()

	env := provider.NewAccountEnv("acc-slow", "openai-compat")
	env.SetCredential("base_url", upstream.URL)
	env.SetCredential("api_key", "sk-test")
	g.Pool().Add(env)
	g.models.Rebuild()

	// 关键：把首字节超时设成 0 = 不限。
	zero := 0
	g.settingsLockedSet(func() { g.settings.FirstByteTimeoutSec = &zero })
	g.applyFailoverSettings()
	if g.cfg.FirstByteTimeout >= 0 {
		t.Fatalf("0 应映射为不限（负值哨兵），实际 %v", g.cfg.FirstByteTimeout)
	}

	srv := httptest.NewServer(g)
	defer srv.Close()

	body := `{"model":"openai-compat/gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，want 200", resp.StatusCode)
	}
	var sb strings.Builder
	buf := make([]byte, 2048)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	if !strings.Contains(sb.String(), "[DONE]") {
		t.Fatalf("响应不完整：%s", truncateForTest(sb.String(), 200))
	}
}
