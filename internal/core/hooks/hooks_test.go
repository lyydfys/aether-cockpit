package hooks

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

// 注册顺序即执行顺序；同名只注册一次。
func TestRegistryOrderAndDedup(t *testing.T) {
	r := NewRegistry()
	if !r.Register(DropReasoning{}) {
		t.Fatal("首次注册应成功")
	}
	if r.Register(DropReasoning{}) {
		t.Fatal("同名重复注册应被忽略")
	}
	if got := r.Names(); len(got) != 1 || got[0] != "drop_reasoning" {
		t.Fatalf("Names = %v", got)
	}
	if !r.Unregister("drop_reasoning") || r.Unregister("drop_reasoning") {
		t.Fatal("Unregister 幂等性不对")
	}
}

// OnRequest 返回 error 时必须拒绝请求（这是"拦截"语义）。
func TestRunRequestBlocks(t *testing.T) {
	r := NewRegistry()
	r.Register(BlockModels{Models: []string{"gpt-*"}})
	req := &ir.UnifiedChatRequest{Model: "gpt-4o"}
	if err := r.RunRequest(context.Background(), req); err == nil {
		t.Fatal("应被拦截")
	}
	ok := &ir.UnifiedChatRequest{Model: "claude-sonnet-4-6"}
	if err := r.RunRequest(context.Background(), ok); err != nil {
		t.Fatalf("不该被拦截：%v", err)
	}
}

// OnProvider / OnAccount 的 skip 语义。
func TestRunProviderAndAccountSkip(t *testing.T) {
	r := NewRegistry()
	r.Register(GuardProvider{Skip: []string{"expensive"}})
	ctx := context.Background()
	req := &ir.UnifiedChatRequest{Model: "m"}
	if !r.RunProvider(ctx, req, "expensive") {
		t.Fatal("应跳过指定渠道")
	}
	if r.RunProvider(ctx, req, "cheap") {
		t.Fatal("不该跳过其它渠道")
	}
}

// OnEvent 的 drop 语义。
func TestRunEventDrops(t *testing.T) {
	r := NewRegistry()
	r.Register(DropReasoning{})
	ctx := context.Background()
	ev := ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: "思考"}
	if !r.RunEvent(ctx, &ev) {
		t.Fatal("推理增量应被丢弃")
	}
	text := ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "正文"}
	if r.RunEvent(ctx, &text) {
		t.Fatal("正文不该被丢弃")
	}
}

// 钩子 panic 不能把请求带崩（fail-open）。
func TestHookPanicIsContained(t *testing.T) {
	r := NewRegistry()
	r.Register(panicHook{})
	ctx := context.Background()
	if err := r.RunRequest(ctx, &ir.UnifiedChatRequest{Model: "m"}); err != nil {
		t.Fatalf("panic 应被吞掉：%v", err)
	}
	ev := ir.UnifiedStreamEvent{Type: ir.EventTextDelta}
	if r.RunEvent(ctx, &ev) {
		t.Fatal("panic 的钩子不该产生 drop")
	}
}

type panicHook struct{ Base }

func (panicHook) Name() string { return "panic_hook" }

func (panicHook) OnRequest(context.Context, *ir.UnifiedChatRequest) error {
	panic("boom")
}

func (panicHook) OnEvent(context.Context, *ir.UnifiedStreamEvent) bool {
	panic("boom")
}

// 钩子阻塞时超时返回，不拖住请求。
func TestHookTimeoutIsContained(t *testing.T) {
	r := NewRegistry()
	r.Register(slowHook{})
	start := time.Now()
	_ = r.RunRequest(context.Background(), &ir.UnifiedChatRequest{Model: "m"})
	if elapsed := time.Since(start); elapsed > hookTimeout*4 {
		t.Fatalf("超时保护没生效，耗时 %v", elapsed)
	}
}

type slowHook struct{ Base }

func (slowHook) Name() string { return "slow_hook" }

func (slowHook) OnRequest(context.Context, *ir.UnifiedChatRequest) error {
	time.Sleep(2 * hookTimeout)
	return nil
}

// ClampOutput 会补上/压低 max_output_tokens。
func TestClampOutput(t *testing.T) {
	hook := ClampOutput{Max: 4096}
	req := &ir.UnifiedChatRequest{}
	if err := hook.OnRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if req.MaxOutputTokens == nil || *req.MaxOutputTokens != 4096 {
		t.Fatalf("未设置上限：%v", req.MaxOutputTokens)
	}
	big := 99999
	req2 := &ir.UnifiedChatRequest{MaxOutputTokens: &big}
	_ = hook.OnRequest(context.Background(), req2)
	if req2.MaxOutputTokens == nil || *req2.MaxOutputTokens != 4096 {
		t.Fatalf("未压低上限：%v", *req2.MaxOutputTokens)
	}
	small := 100
	req3 := &ir.UnifiedChatRequest{MaxOutputTokens: &small}
	_ = hook.OnRequest(context.Background(), req3)
	if *req3.MaxOutputTokens != 100 {
		t.Fatalf("不该抬高已有上限：%d", *req3.MaxOutputTokens)
	}
}

// OnUsage 回调真的被调用（用量扩展点）。
func TestUsageCallbackFires(t *testing.T) {
	var hits atomic.Int32
	r := NewRegistry()
	r.Register(UsageCallback{Fn: func(s UsageSummary) {
		if s.Model == "m" {
			hits.Add(1)
		}
	}})
	r.RunUsage(context.Background(), UsageSummary{Model: "m"})
	time.Sleep(10 * time.Millisecond) // 钩子跑在独立 goroutine（超时保护）
	if hits.Load() != 1 {
		t.Fatalf("回调次数 = %d", hits.Load())
	}
}

// 拦截错误的类型必须可判别（网关据此返回 4xx 而不是 500）。
func TestBlockedErrorType(t *testing.T) {
	err := (&BlockedError{Pattern: "gpt-*", Model: "gpt-4o"}).Error()
	if err == "" {
		t.Fatal("错误信息不能为空")
	}
	var target *BlockedError
	r := NewRegistry()
	r.Register(BlockModels{Models: []string{"gpt-*"}})
	got := r.RunRequest(context.Background(), &ir.UnifiedChatRequest{Model: "gpt-4o"})
	if !errors.As(got, &target) {
		t.Fatalf("应返回 *BlockedError，得到 %T", got)
	}
}
