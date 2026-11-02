package gateway

import (
	"context"
	"encoding/json"
	"fmt"
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

// contextTrimProvider 第一次看到超长上下文时返回 400，模拟上游的
// "context length exceeded"；裁剪后必须能正常返回。
type contextTrimProvider struct {
	mu   sync.Mutex
	seen int
}

func (p *contextTrimProvider) Manifest() provider.ProviderManifest {
	return provider.ProviderManifest{
		ID:             "context-trim-test",
		DisplayName:    "Context Trim Test",
		Family:         "test",
		NetworkPolicy:  provider.NetPolicySystem,
		SupportedAuths: []provider.AuthMethod{provider.AuthManualInput},
		Models: []ir.ModelMeta{{
			ID:            "context-trim-test/echo",
			UpstreamModel: "echo",
			DisplayName:   "Echo",
			Capabilities:  ir.ModelCapabilities{Streaming: true},
		}},
		Capabilities: provider.CapabilitySet{Chat: true},
	}
}

func (p *contextTrimProvider) ChatStream(ctx context.Context, env *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
	if len(req.Items) > contextKeepTailItems {
		return nil, ir.NewAPIError(ir.ErrCodeBadRequest, "context length exceeded")
	}
	p.mu.Lock()
	p.seen = len(req.Items)
	p.mu.Unlock()
	out := make(chan ir.UnifiedStreamEvent, 2)
	out <- ir.UnifiedStreamEvent{Type: ir.EventStart, ResponseID: "resp_trim"}
	out <- ir.UnifiedStreamEvent{Type: ir.EventDone, FinishReason: "stop"}
	close(out)
	return out, nil
}

func (p *contextTrimProvider) Seen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen
}

var _ = func() bool {
	provider.Register(&contextTrimProvider{})
	return true
}()

// TestContextContinueOnIRPath 锁住 P2：/v1/messages 走 IR 路径时，
// 上游报上下文超限也必须裁剪重试，而不是把 400 直接甩给客户端。
func TestContextContinueOnIRPath(t *testing.T) {
	g := New(Config{
		MaxAttempts:             2,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:               "-",
		MaxRecords:              20,
	})
	env := provider.NewAccountEnv("context-trim-acc", "context-trim-test")
	g.Pool().Add(env)
	g.models.Rebuild()

	msgs := make([]map[string]string, 0, 100)
	for i := 0; i < 100; i++ {
		msgs = append(msgs, map[string]string{"role": "user", "content": fmt.Sprintf("message-%d", i)})
	}
	body, _ := json.Marshal(map[string]any{
		"model": "context-trim-test/echo", "max_tokens": 32, "messages": msgs,
	})
	srv := httptest.NewServer(g)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/messages", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, raw)
	}
	p, ok := provider.Get("context-trim-test")
	if !ok {
		t.Fatal("测试渠道未注册")
	}
	seen := p.(*contextTrimProvider).Seen()
	if seen == 0 || seen > contextKeepTailItems {
		t.Fatalf("上游收到 %d 条消息，期望裁剪到 <= %d", seen, contextKeepTailItems)
	}
}