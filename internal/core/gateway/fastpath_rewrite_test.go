package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/mw"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// fastPathFloatPtr 只在本文件用，避免和同包其它测试的 helper 重名。
func fastPathFloatPtr(v float64) *float64 { return &v }

// fastPathTestGateway 建一个带测试渠道账号的网关，并把规则/ spec 的存储
// 都指到临时目录：这些测试会写字，绝不能碰到真实数据目录。
func fastPathTestGateway(t *testing.T) *Gateway {
	t.Helper()
	dir := t.TempDir()
	g := New(Config{
		MaxAttempts:             1,
		FirstByteTimeout:        5 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(dir, "settings.json"),
		AccountStorePath:        filepath.Join(dir, "accounts.json"),
		MiddlewarePath:          filepath.Join(dir, "middleware.json"),
		UsagePath:               "-",
		MaxRecords:              20,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = g.Close(ctx)
	})

	env := provider.NewAccountEnv("fastpath-rewrite-acc", "redact-native-test")
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	g.Pool().Add(env)
	g.models.Rebuild()
	return g
}

// fastPathRequestBody 是测试渠道能识别的 Responses 请求体（流式与非流式都走直通）。
func fastPathRequestBody() []byte {
	return []byte(`{"model":"redact-native-test/echo","stream":true,"input":"hi"}`)
}

// TestFastPathSkipsWhenRewriteRuleEnabled 锁住 P1-4 回归：
// 改写规则只在 IR 路径的 rewrite 中间件里执行，直通必须让路，
// 否则用户打开 param_override / model_map / word_guard / system_prompt 后
// 看到的永远是"规则都在、就是不起作用"。
func TestFastPathSkipsWhenRewriteRuleEnabled(t *testing.T) {
	g := fastPathTestGateway(t)
	body := fastPathRequestBody()

	// 对照组：没配任何规则时直通应当放行。
	// 少了这一步，下面的失败断言就无法证明是"规则"挡住的。
	if !g.tryFastPath(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)),
		ir.ProtocolOpenAIResponses, body, &RequestLog{}, nil) {
		t.Fatal("没有改写规则时不该挡直通")
	}

	if err := g.rewriteEngine().Upsert(mw.Rule{
		ID: "p1", Kind: mw.KindParamOverride, Name: "统一温度", Enabled: true, Temperature: fastPathFloatPtr(0.2),
	}); err != nil {
		t.Fatalf("写入规则失败: %v", err)
	}
	if !g.rewriteEngine().Enabled() {
		t.Fatal("规则引擎未报告启用")
	}

	if g.tryFastPath(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)),
		ir.ProtocolOpenAIResponses, body, &RequestLog{}, nil) {
		t.Fatal("有启用改写规则时不能直通（规则会被静默跳过）")
	}

	// 规则关掉后直通恢复：挡的必须是"启用的规则"，不是引擎对象本身。
	if err := g.rewriteEngine().Upsert(mw.Rule{
		ID: "p1", Kind: mw.KindParamOverride, Name: "统一温度", Enabled: false, Temperature: fastPathFloatPtr(0.2),
	}); err != nil {
		t.Fatalf("改写规则失败: %v", err)
	}
	if !g.tryFastPath(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)),
		ir.ProtocolOpenAIResponses, body, &RequestLog{}, nil) {
		t.Fatal("规则关闭后应恢复直通")
	}
}

// TestFastPathSkipsWhenSpecOverrides 锁住另一半：
// spec 声明的 system_prompt_inject / request_overrides 由 IR 路径的
// spec_overrides 中间件落地，直通读不到它们，所以该渠道必须退回 IR。
func TestFastPathSkipsWhenSpecOverrides(t *testing.T) {
	prev := spec.Default()
	st := spec.NewStore()
	spec.SetDefault(st)
	t.Cleanup(func() { spec.SetDefault(prev) })

	g := fastPathTestGateway(t)
	body := fastPathRequestBody()

	dir := t.TempDir()
	sp := `{"provider_id":"redact-native-test","system_prompt_inject":"你必须始终用中文回答。"}`
	if err := os.WriteFile(filepath.Join(dir, "spec.json"), []byte(sp), 0o644); err != nil {
		t.Fatalf("写 spec 失败: %v", err)
	}
	if err := st.LoadDir(dir); err != nil {
		t.Fatalf("加载 spec 失败: %v", err)
	}

	if g.tryFastPath(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)),
		ir.ProtocolOpenAIResponses, body, &RequestLog{}, nil) {
		t.Fatal("spec 声明了请求体改写时不能直通")
	}
}

// TestHandlersCacheInvalidatedOnSpecChange 锁住 P1-4 的第二个回归：
// 管道是按 Manifest 的中间件清单组装的，spec 热更新改了清单后缓存必须作废，
// 否则新清单要等重启才生效。
func TestHandlersCacheInvalidatedOnSpecChange(t *testing.T) {
	prev := spec.Default()
	st := spec.NewStore()
	spec.SetDefault(st)
	t.Cleanup(func() { spec.SetDefault(prev) })

	g := fastPathTestGateway(t)

	p, ok := provider.Get("redact-native-test")
	if !ok {
		t.Fatal("测试渠道未注册")
	}
	chat, ok := p.(provider.ChatCapability)
	if !ok {
		t.Fatal("测试渠道未实现 ChatStream")
	}
	g.handlerFor(p, chat)
	if _, ok := g.handlers.Load(p.Manifest().ID); !ok {
		t.Fatal("管道未写入缓存")
	}

	// 走真实的 spec 热更新入口，而不是直接调 invalidateHandlers：
	// 要断言的是"变更回调里接上了失效"，不是"这个函数本身能删 key"。
	dir := t.TempDir()
	sp := `{"provider_id":"redact-native-test","middlewares":["system_role_merge"]}`
	if err := os.WriteFile(filepath.Join(dir, "spec.json"), []byte(sp), 0o644); err != nil {
		t.Fatalf("写 spec 失败: %v", err)
	}
	if err := st.LoadDir(dir); err != nil {
		t.Fatalf("加载 spec 失败: %v", err)
	}

	if _, ok := g.handlers.Load(p.Manifest().ID); ok {
		t.Fatal("spec 变更后管道缓存未失效，中间件清单热更新不会生效")
	}
}
