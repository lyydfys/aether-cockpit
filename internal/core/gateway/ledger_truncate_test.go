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

// 上游只报 "prompt_tokens": 0（表示"没统计"）时，绝不能判定为截断。
//
// 真实数据：open-design 的流式 usage 恒为
// {"completion_tokens":N, "prompt_tokens":0}，旧逻辑把 0 当成实际输入量，
// 于是估算 20 万 token 的请求全部被标成"上游截断"——200 条里 184 条误报。
func TestLedgerDoesNotFlagWhenUpstreamOmitsPromptTokens(t *testing.T) {
	p := &ledgerPending{}
	p.noteEstimate(204351)
	p.noteUsage(&ir.Usage{InputTokens: 0, OutputTokens: 40}, 0, 0)
	if p.rec.UpstreamTruncated {
		t.Fatal("上游没报输入 token 时不应判定为截断（那是没统计，不是截断）")
	}

	// nil usage 同理。
	q := &ledgerPending{}
	q.noteEstimate(200000)
	q.noteUsage(nil, 0, 0)
	if q.rec.UpstreamTruncated {
		t.Fatal("没有 usage 时不应判定截断")
	}
}

// 上游不报输入 token 时，In 用本地估算回填（主流网关的通行做法），
// 但要打上 InEstimated 标记，且不能据此判定截断。
func TestLedgerBackfillsInputTokensWhenUpstreamOmits(t *testing.T) {
	p := &ledgerPending{}
	p.noteEstimate(204351)
	p.noteUsage(&ir.Usage{InputTokens: 0, OutputTokens: 40}, 0, 0)

	if p.rec.In != 204351 {
		t.Fatalf("In 应回填为估算值 204351，实际 %d", p.rec.In)
	}
	if !p.rec.InEstimated {
		t.Fatal("回填后应标记 InEstimated，供界面区分统计值与估算值")
	}
	if p.rec.UpstreamTruncated {
		t.Fatal("估算回填不应触发截断判定")
	}
}

// 上游正常报 token 时不回填、不标记。
func TestLedgerKeepsReportedInputTokens(t *testing.T) {
	p := &ledgerPending{}
	p.noteEstimate(360000)
	p.noteUsage(&ir.Usage{InputTokens: 300000, CachedTokens: 30000}, 0, 0)
	if p.rec.In != 300000 {
		t.Fatalf("In 应为上游报的 300000，实际 %d", p.rec.In)
	}
	if p.rec.InEstimated {
		t.Fatal("上游有真实数值时不应标记为估算")
	}
}
