package gateway

import (
	"fmt"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 性能基线：报告指出全仓此前没有任何 Go benchmark。
//
// 这些测试不追求绝对数字（不同机器差异很大），目的是提供**可回归的基线**：
// 改动代理热路径后跑 `go test -bench . ./internal/core/gateway/`，
// 与上一次结果对比即可发现"某次重构让每请求多复制了一次 body"这类退化。

// BenchmarkUsageRecord 是每个请求结束都会走一次的记账路径。
// 锁内只做 map 更新，不应有任何序列化或 IO。
func BenchmarkUsageRecord(b *testing.B) {
	s := newUsageStore(filepath.Join(b.TempDir(), "usage.json"))
	s.maxDays = 7
	cell := usageCell{
		Date: "2026-11-08", ProviderID: "prov", Model: "model",
		AccountID: "acct", Requests: 1, InputTokens: 100, OutputTokens: 200,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.record(cell)
	}
}

// BenchmarkUsageFlushSnapshot 测落盘成本（锁内快照 + 锁外序列化）。
// 这是周期任务，但若快照变贵会直接拖慢记账的临界区。
func BenchmarkUsageFlushSnapshot(b *testing.B) {
	s := newUsageStore(filepath.Join(b.TempDir(), "usage.json"))
	s.maxDays = 30
	// 填充到接近真实规模：30 天 × 每天若干渠道/账号组合。
	for d := 0; d < 30; d++ {
		date := fmt.Sprintf("2026-10-%02d", d%28+1)
		for p := 0; p < 8; p++ {
			for a := 0; a < 4; a++ {
				s.record(usageCell{
					Date: date, ProviderID: fmt.Sprintf("p%d", p), Model: "m",
					AccountID: fmt.Sprintf("a%d", a), Requests: 1, InputTokens: 10, OutputTokens: 20,
				})
			}
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.flush()
	}
}

// BenchmarkUsageSnapshotOnly 单独测"锁内快照"的成本，
// 用来证明记账临界区本身很轻（不含 IO）。
func BenchmarkUsageSnapshotOnly(b *testing.B) {
	s := newUsageStore(filepath.Join(b.TempDir(), "usage.json"))
	s.maxDays = 30
	for d := 0; d < 30; d++ {
		date := fmt.Sprintf("2026-10-%02d", d%28+1)
		for p := 0; p < 8; p++ {
			for a := 0; a < 4; a++ {
				s.record(usageCell{
					Date: date, ProviderID: fmt.Sprintf("p%d", p), Model: "m",
					AccountID: fmt.Sprintf("a%d", a), Requests: 1,
				})
			}
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.mu.Lock()
		snap := s.snapshotLocked()
		s.mu.Unlock()
		_ = snap
	}
}

// BenchmarkDecideEdgeSwitch 是每轮线路扫描都会调的纯函数，应保持极低开销。
func BenchmarkDecideEdgeSwitch(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = decideEdgeSwitch(300, 3, 150, 150)
	}
}

// BenchmarkMedianInts 线路健康采样每次都算，规模小但频率高。
func BenchmarkMedianInts(b *testing.B) {
	samples := []int64{120, 340, 155, 900, 210, 178, 260}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = medianInts(samples)
	}
}

// BenchmarkIsContentEvent 在流事件热路径上每事件一次。
func BenchmarkIsContentEvent(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isContentEvent(ir.EventTextDelta)
	}
}

// BenchmarkSessionFingerprint 选号前每个请求都要算（会话粘性命中率相关）。
func BenchmarkRequestClone(b *testing.B) {
	req := &ir.UnifiedChatRequest{
		Model:  "deepseek-v4.1-flash",
		Stream: true,
		Items: []ir.UnifiedItem{
			{Type: ir.ItemTypeMessage, Role: "system", Text: "you are a helpful assistant"},
			{Type: ir.ItemTypeMessage, Role: "user", Text: "帮我分析这段代码的性能问题"},
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = req.Clone()
	}
}
