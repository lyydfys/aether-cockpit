package gateway

import (
	"context"
	"log"
)

// goBackground 把后台循环登记进统一 waitgroup；Close 会等待它们退出。
// Close 开始后拒绝新登记，避免 waitgroup 在归零后又被 Add。
func (g *Gateway) goBackground(fn func()) {
	g.bgMu.Lock()
	if g.bgClosed {
		g.bgMu.Unlock()
		return
	}
	g.bg.Add(1)
	g.bgMu.Unlock()
	go func() {
		defer g.bg.Done()
		fn()
	}()
}

// Close 取消生命周期 context 并等待所有已登记的后台循环退出。
// 可重复调用；ctx 只限制本次等待，循环退出后再次调用仍可成功。
func (g *Gateway) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	g.closeOnce.Do(func() {
		if g.cancel != nil {
			g.cancel()
		}
		if g.keepAwakeOn.Swap(false) {
			if err := g.applyKeepAwake(false); err != nil {
				log.Printf("[WARN] 关闭保持唤醒失败: %v", err)
			}
		}
	})
	g.bgMu.Lock()
	g.bgClosed = true
	g.bgMu.Unlock()
	done := make(chan struct{})
	go func() {
		g.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
		g.storeCloseOnce.Do(func() {
			if g.store != nil {
				if err := g.store.Close(); err != nil {
					log.Printf("[WARN] 关闭状态库失败: %v", err)
				}
			}
		})
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *Gateway) applyKeepAwake(on bool) error {
	if g.keepAwakeApply != nil {
		return g.keepAwakeApply(on)
	}
	return setKeepAwake(on)
}
