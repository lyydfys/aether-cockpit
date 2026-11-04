package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/pool"
	"aether-cockpit/internal/provider"
)

// 默认档位：open-design 这类跨境/中转渠道开预热与并发闸，本地渠道保持原样。
func TestChannelNetDefaults(t *testing.T) {
	s := newChannelNetStore()
	p := s.Get("open-design")
	if p.PrewarmSec <= 0 || p.MaxInflight <= 0 {
		t.Fatalf("open-design 应有默认优化档位：%+v", p)
	}
	if q := s.Get("ollama"); q != (ChannelNetProfile{}) {
		t.Fatalf("未声明的渠道不该被优化：%+v", q)
	}
	// 覆盖后生效；置零表示关掉。
	s.Set("open-design", ChannelNetProfile{MaxInflight: 1})
	if got := s.Get("open-design"); got.MaxInflight != 1 || got.PrewarmSec != 0 {
		t.Fatalf("覆盖失败：%+v", got)
	}
	s.Set("open-design", ChannelNetProfile{})
	if got := s.Get("open-design"); got != (ChannelNetProfile{}) {
		t.Fatalf("置零应删除档位：%+v", got)
	}
}

// 未满时立即拿到槽位；满了最多等 wait，然后放行。
func TestInflightGateCapsAndBails(t *testing.T) {
	g := newInflightGate()
	r1 := g.acquire("p", 1, 0)
	if g.inFlight("p") != 1 {
		t.Fatalf("应占 1 个槽位，得到 %d", g.inFlight("p"))
	}
	start := time.Now()
	r2 := g.acquire("p", 1, 50*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Fatalf("满槽时应等待，实际 %v", elapsed)
	}
	if elapsed > 400*time.Millisecond {
		t.Fatalf("等待应有上限，实际 %v", elapsed)
	}
	r2() // 超时放行的 release 是空操作
	if g.inFlight("p") != 1 {
		t.Fatalf("超时放行不该改变槽位计数，得到 %d", g.inFlight("p"))
	}
	r1()
	if g.inFlight("p") != 0 {
		t.Fatalf("释放后应为 0，得到 %d", g.inFlight("p"))
	}
}

// 槽位随流关闭释放（不是随 openStream 返回释放）。
func TestReleaseWhenClosedReleasesAfterStream(t *testing.T) {
	var released atomic.Bool
	in := make(chan ir.UnifiedStreamEvent, 2)
	out := releaseWhenClosed(in, func() { released.Store(true) })
	in <- ir.UnifiedStreamEvent{Type: ir.EventTextDelta, TextDelta: "a"}
	if _, ok := <-out; !ok {
		t.Fatal("应收到事件")
	}
	if released.Load() {
		t.Fatal("流还没结束就释放了槽位")
	}
	close(in)
	for range out {
	}
	// 转发器在 close(out) 之前 defer release()，这里等它落定。
	deadline := time.Now().Add(time.Second)
	for !released.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !released.Load() {
		t.Fatal("流结束后应释放槽位")
	}
}

// 预热探针打的是 base_url + /models，并带上 Bearer Key。
func TestPrewarmOnceHitsModels(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	env := provider.NewAccountEnv("acc-1", "open-design")
	env.Credentials["api_key"] = "sk-test"
	env.HTTPClient = srv.Client()

	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.prewarmOnce(env, srv.URL)

	if gotPath != "/models" {
		t.Errorf("探针路径 = %q，应为 /models", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("探针鉴权 = %q", gotAuth)
	}
}

// 账号填了 base_url 时以账号为准；没填用渠道默认。
func TestChannelBaseURLPrecedence(t *testing.T) {
	env := provider.NewAccountEnv("acc", "open-design")
	if got := channelBaseURL(env, "https://default.example/v1"); got != "https://default.example/v1" {
		t.Fatalf("没填 base_url 应用默认：%q", got)
	}
	env.Credentials["base_url"] = "https://custom.example/v1/"
	if got := channelBaseURL(env, "https://default.example/v1"); got != "https://custom.example/v1" {
		t.Fatalf("账号 base_url 应优先且去掉末尾斜杠：%q", got)
	}
}

// 预热只挑一个可用账号，不会在多账号渠道上打出成倍探针。
func TestWarmChannelsOncePicksOneAccount(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	g.pool = pool.New()
	for i := 0; i < 3; i++ {
		env := provider.NewAccountEnv("acc-"+string(rune('a'+i)), "open-design")
		// 指到测试服务器：这个用例不该碰真实上游。
		env.Credentials["base_url"] = srv.URL
		env.HTTPClient = srv.Client()
		g.pool.Add(env)
	}

	last := map[string]time.Time{}
	// 直接验证选账号逻辑：一轮只应该打 1 次（只预热一个账号）。
	g.warmChannelsOnce(last)
	first := hits.Load()
	for i := 0; i < 3; i++ {
		g.warmChannelsOnce(last)
	}
	if first > 1 {
		t.Fatalf("一轮最多打 1 次探针，实际 %d", first)
	}
	if hits.Load() != first {
		t.Fatalf("间隔未到不该重复预热：%d -> %d", first, hits.Load())
	}
}

// 预热循环可被停止（不泄漏 goroutine）。
func TestStartChannelWarmStops(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	ctx, cancel := context.WithCancel(context.Background())
	stop := g.StartChannelWarm(ctx, 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	stop()
	cancel()
	time.Sleep(20 * time.Millisecond)
}
