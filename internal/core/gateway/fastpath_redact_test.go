package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// redactNativeProvider 是一个测试专用的原生 Responses 渠道：
// 它同时实现 ChatStream 与 ResponsesPassthrough，并记录上游实际收到的内容。
type redactNativeProvider struct {
	mu   sync.Mutex
	seen string
}

func (p *redactNativeProvider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:             "redact-native-test",
		DisplayName:    "Redact Native Test",
		Family:         "test",
		NetworkPolicy:  provider.NetPolicySystem,
		SupportedAuths: []provider.AuthMethod{provider.AuthManualInput},
		Models: []ir.ModelMeta{
			{
				ID:            "redact-native-test/echo",
				UpstreamModel: "echo",
				DisplayName:   "Echo",
				Capabilities:  ir.ModelCapabilities{Streaming: true},
			},
		},
		NativeResponses: true,
		Capabilities:    provider.CapabilitySet{Chat: true},
	}
}

func (p *redactNativeProvider) setSeen(s string) {
	p.mu.Lock()
	p.seen = s
	p.mu.Unlock()
}

func (p *redactNativeProvider) Seen() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen
}

func (p *redactNativeProvider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	raw, _ := json.Marshal(req)
	p.setSeen(string(raw))

	out := make(chan ir.UnifiedStreamEvent, 3)
	out <- ir.UnifiedStreamEvent{Type: ir.EventStart, ResponseID: "resp_redact_test"}
	out <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "ok"}
	out <- ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: "stop"}
	close(out)
	return out, nil
}

func (p *redactNativeProvider) ResponsesPassthrough(ctx context.Context, env *provider.AccountEnv, rawReq []byte, upstreamModel string) (*http.Response, error) {
	p.setSeen(string(rawReq))
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("event: response.completed\ndata: {}\n\n")),
	}, nil
}

var _ = func() bool {
	provider.Register(&redactNativeProvider{})
	return true
}()

// TestFastPathSkipsWhenRedactEnabled 锁住 P0 回归：
// 只开脱敏规则、不开 privacy_mask 时，Responses 流式请求也不能走 Fast-Path，
// 否则上游会收到原始 secret。
func TestFastPathSkipsWhenRedactEnabled(t *testing.T) {
	g := New(Config{
		MaxAttempts:             1,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:               "-",
		MaxRecords:              20,
	})
	env := provider.NewAccountEnv("redact-native-test-acc", "redact-native-test")
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	g.Pool().Add(env)
	g.models.Rebuild()

	g.redactEngine().SetEnabled(true)
	if !g.redactEngine().Enabled() {
		t.Fatal("脱敏引擎未启用")
	}

	secret := "sk-" + strings.Repeat("A", 24)
	body := `{"model":"redact-native-test/echo","stream":true,"input":"my key is ` + secret + `"}`
	srv := httptest.NewServer(g)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}

	// 测试里注册的是同一个实例；全局注册表拿回来读，避免依赖包级变量。
	p, ok := provider.Get("redact-native-test")
	if !ok {
		t.Fatal("测试渠道未注册")
	}
	seen := p.(*redactNativeProvider).Seen()

	if strings.Contains(seen, secret) {
		t.Fatalf("上游收到了明文 secret（Fast-Path 绕过了脱敏）: %s", seen)
	}
	if !strings.Contains(seen, "{{API_KEY_") {
		t.Fatalf("上游没有收到脱敏占位符: %s", seen)
	}
}
