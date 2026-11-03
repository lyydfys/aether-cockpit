package gateway

import (
	"context"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 钩子是设置驱动的：配了就装上，删了就卸掉（不留幽灵）。
func TestSyncHooksFromSettings(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})

	g.settingsMu.Lock()
	if g.settings.UI == nil {
		g.settings.UI = map[string]any{}
	}
	g.settings.UI["hooks"] = map[string]any{
		"drop_reasoning": true,
		"clamp_output":   float64(2048),
		"block_models":   []any{"gpt-4*"},
	}
	g.settingsMu.Unlock()
	g.syncHooks()

	names := g.hookRegistry().Names()
	if len(names) != 3 {
		t.Fatalf("应装上 3 个钩子，得到 %v", names)
	}

	// 拦截规则真的生效。
	if err := g.hookRegistry().RunRequest(context.Background(), &ir.UnifiedChatRequest{Model: "gpt-4o"}); err == nil {
		t.Fatal("block_models 应拦下 gpt-4o")
	}
	// clamp 生效。
	req := &ir.UnifiedChatRequest{Model: "claude"}
	_ = g.hookRegistry().RunRequest(context.Background(), req)
	if req.MaxOutputTokens == nil || *req.MaxOutputTokens != 2048 {
		t.Fatalf("clamp_output 未生效：%v", req.MaxOutputTokens)
	}

	// 清空配置后钩子必须全部卸掉。
	g.settingsMu.Lock()
	delete(g.settings.UI, "hooks")
	g.settingsMu.Unlock()
	g.syncHooks()
	if got := g.hookRegistry().Names(); len(got) != 0 {
		t.Fatalf("清空配置后不该留钩子：%v", got)
	}
}

// 没有配置时不装任何钩子（默认零开销）。
func TestSyncHooksNoConfig(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.syncHooks()
	if got := g.hookRegistry().Names(); len(got) != 0 {
		t.Fatalf("默认不该有钩子：%v", got)
	}
	// 空注册表时事件流要原样透传（不该多一层 goroutine 拷贝）。
	in := make(chan ir.UnifiedStreamEvent)
	if out := g.withEventHooks(context.Background(), in); out != in {
		t.Fatal("无钩子时应原样返回通道")
	}
}

// 有钩子时事件流经过钩子：推理增量被丢弃，正文原样通过。
func TestWithEventHooksFiltersStream(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.settingsMu.Lock()
	if g.settings.UI == nil {
		g.settings.UI = map[string]any{}
	}
	g.settings.UI["hooks"] = map[string]any{"drop_reasoning": true}
	g.settingsMu.Unlock()
	g.syncHooks()

	in := make(chan ir.UnifiedStreamEvent, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := g.withEventHooks(ctx, in)
	in <- ir.UnifiedStreamEvent{Type: ir.EventReasoningDelta, TextDelta: "想想"}
	in <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "答案"}
	close(in)

	got := []ir.StreamEventType{}
	for ev := range out {
		got = append(got, ev.Type)
	}
	if len(got) != 1 || got[0] != ir.EventTextDelta {
		t.Fatalf("应只剩正文事件，得到 %v", got)
	}
}
