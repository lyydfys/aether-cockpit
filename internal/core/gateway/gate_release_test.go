package gateway

import (
	"context"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
)

// 槽位必须跟着流的生命周期释放：流结束时释放一次。
func TestReleaseOnCloseReleasesOnStreamEnd(t *testing.T) {
	released := make(chan struct{})
	ch := make(chan ir.UnifiedStreamEvent, 2)
	ch <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "a"}
	ch <- ir.UnifiedStreamEvent{Type: ir.EventDone}
	close(ch)

	out := releaseOnClose(context.Background(), ch, func() { close(released) })
	n := 0
	for range out {
		n++
	}
	if n != 2 {
		t.Fatalf("转发事件数 = %d, want 2", n)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("流结束后未释放并发槽位")
	}
}

// 客户端断开后不再下发，但仍要读空上游并释放槽位，否则槽位永久泄漏。
func TestReleaseOnCloseDrainsAfterCancel(t *testing.T) {
	released := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan ir.UnifiedStreamEvent, 1)
	ch <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "a"}

	out := releaseOnClose(ctx, ch, func() { close(released) })
	cancel() // 客户端断开：不再消费 out

	go func() {
		time.Sleep(20 * time.Millisecond)
		ch <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "b"}
		close(ch)
	}()

	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("断流后槽位未释放（上游已关闭）")
	}
	_ = out
}
