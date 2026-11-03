package pipeline

import (
	"context"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/mw"
	"aether-cockpit/internal/provider"
)

// 模型映射必须改到上游真正读取的 UpstreamModel，而不只是 req.Model。
func TestRewriteModelMapUpdatesUpstreamModel(t *testing.T) {
	eng := mw.New("")
	if err := eng.Upsert(mw.Rule{
		ID: "map-1", Kind: mw.KindModelMap, Name: "map", Enabled: true,
		From: "virtual-*", To: "upstream-model",
	}); err != nil {
		t.Fatal(err)
	}
	var gotModel, gotUpstream string
	next := func(_ context.Context, _ *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
		gotModel, gotUpstream = req.Model, req.UpstreamModel
		return nil, nil
	}
	h := Rewrite(Deps{Rewrite: eng})(next)
	req := &ir.UnifiedChatRequest{Model: "virtual-x", UpstreamModel: "virtual-x", ProviderID: "p1"}
	if _, err := h(context.Background(), nil, req); err != nil {
		t.Fatal(err)
	}
	if gotModel != "upstream-model" || gotUpstream != "upstream-model" {
		t.Fatalf("model=%q upstream=%q", gotModel, gotUpstream)
	}
}
