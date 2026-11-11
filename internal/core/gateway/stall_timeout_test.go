package gateway

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// StallTimeoutMS 必须真的生效，不能只是配置项。
//
// 历史状态：这个字段在 channel_net.go 里定义、能被设置、也会出现在 API 里，
// 但全仓没有任何执行逻辑读它——配了等于没配。open-design 这类跨线路上游
// 明明标了 8000ms，实际仍然等满全局 60 秒。
func TestStallTimeoutIsEnforced(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
		FirstByteTimeout: 30 * time.Second, // 全局很宽松
		MaxAttempts:      1,
		RequestBudget:    time.Minute,
	})
	// 该渠道的停滞阈值设成 120ms，应当远早于全局 30s 触发。
	g.channelNet().Set("openai-compat", ChannelNetProfile{StallTimeoutMS: 120})

	env := provider.NewAccountEnv("acc-stall", "openai-compat")
	blocked := make(chan struct{})
	defer close(blocked)
	h := func(ctx context.Context, e *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
		<-blocked // 一直不返回
		return nil, context.DeadlineExceeded
	}

	start := time.Now()
	_, err := g.openStream(context.Background(), context.Background(), h, env,
		&ir.UnifiedChatRequest{Model: "m", Stream: true}, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("停滞时应返回超时错误")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("StallTimeoutMS 没生效（应约 120ms 就放弃），耗时 %v", elapsed)
	}
	ae := ir.AsAPIError(err)
	if ae == nil || !ae.Retryable {
		t.Fatalf("停滞超时应可重试（换线路）：%+v", err)
	}
}

// 没有配 StallTimeoutMS 时，仍然按全局 FirstByteTimeout 走（不能被误改成 0 就立刻超时）。
func TestNoStallTimeoutFallsBackToGlobal(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
		FirstByteTimeout: 300 * time.Millisecond,
		MaxAttempts:      1,
		RequestBudget:    time.Minute,
	})
	env := provider.NewAccountEnv("acc-nostall", "openai-compat")
	blocked := make(chan struct{})
	defer close(blocked)
	h := func(ctx context.Context, e *provider.AccountEnv, req *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
		<-blocked
		return nil, context.DeadlineExceeded
	}
	start := time.Now()
	_, err := g.openStream(context.Background(), context.Background(), h, env,
		&ir.UnifiedChatRequest{Model: "m", Stream: true}, nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("应超时")
	}
	// 应该在 300ms 附近，而不是立即（说明 stall=0 被正确处理为"不覆盖"）。
	if elapsed < 200*time.Millisecond {
		t.Fatalf("不该早于全局超时，耗时 %v", elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("全局超时没生效，耗时 %v", elapsed)
	}
}

// 渠道档位设置/读取的往返（确认 Set 真的写进去了，测试辅助本身可靠）。
func TestChannelNetStallTimeoutRoundTrip(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	g.channelNet().Set("custom-x", ChannelNetProfile{StallTimeoutMS: 8000, MaxInflight: 3})
	got := g.channelNet().Get("custom-x")
	if got.StallTimeoutMS != 8000 {
		t.Fatalf("StallTimeoutMS = %d，want 8000", got.StallTimeoutMS)
	}
	if got.MaxInflight != 3 {
		t.Fatalf("MaxInflight = %d，want 3", got.MaxInflight)
	}
}
