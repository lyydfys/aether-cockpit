package gateway

import (
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 上游静默截断必须被标记：估算 36 万、上游只认 1381，差两个数量级。
func TestLedgerMarksUpstreamTruncated(t *testing.T) {
	p := &ledgerPending{}
	p.noteEstimate(360000)
	p.noteUsage(&ir.Usage{InputTokens: 1381}, 0, 0)
	if !p.rec.UpstreamTruncated {
		t.Fatal("应标记 upstream_truncated")
	}

	// 正常情况不能误标（估算 36 万，实际上游认了 33 万）。
	ok := &ledgerPending{}
	ok.noteEstimate(360000)
	ok.noteUsage(&ir.Usage{InputTokens: 300000, CachedTokens: 30000}, 0, 0)
	if ok.rec.UpstreamTruncated {
		t.Fatal("正常请求不应标记截断")
	}

	// 小请求不做判定，避免估算误差造成误报。
	small := &ledgerPending{}
	small.noteEstimate(3000)
	small.noteUsage(&ir.Usage{InputTokens: 100}, 0, 0)
	if small.rec.UpstreamTruncated {
		t.Fatal("小请求不应判定截断")
	}
}
