package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/core/spec"
	"aether-cockpit/internal/provider"
)

// --- 测试脚手架 ---

// fakeSpecJSON 一条带完整补偿配置的假 spec，走真实 JSON 解析路径，
// 顺带验证 system_prompt_inject / request_overrides 两个键的 tag 拼写。
const fakeSpecJSON = `{
	"provider_id": "fake",
	"spec_version": "t1",
	"system_prompt_inject": "渠道注入的系统提示",
	"request_overrides": {"max_output_tokens": 4096, "temperature": 0.3}
}`

// installSpecStore 把全局 spec 存储换成只含 content 这条 spec 的临时存储。
// content 为空字符串表示"一个 spec 都没有"。
func installSpecStore(t *testing.T, content string) {
	t.Helper()
	s := spec.NewStore()
	if content != "" {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "fake.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.LoadDir(dir); err != nil {
			t.Fatalf("加载假 spec 失败: %v", err)
		}
	}
	old := spec.Default()
	spec.SetDefault(s)
	t.Cleanup(func() { spec.SetDefault(old) })
}

// runThroughMiddleware 组装只含 spec_overrides 的管道并执行，
// 返回 next 实际收到的请求。本身同时就是链路测试。
func runThroughMiddleware(t *testing.T, req *ir.UnifiedChatRequest) *ir.UnifiedChatRequest {
	t.Helper()
	var got *ir.UnifiedChatRequest
	base := func(ctx context.Context, env *provider.AccountEnv, r *ir.UnifiedChatRequest) (<-chan ir.UnifiedStreamEvent, error) {
		got = r
		ch := make(chan ir.UnifiedStreamEvent, 1)
		close(ch)
		return ch, nil
	}
	h, missing := Build(base, Deps{}, []string{"spec_overrides"})
	if len(missing) != 0 {
		t.Fatalf("spec_overrides 未注册: missing=%v", missing)
	}
	if _, err := h(context.Background(), nil, req); err != nil {
		t.Fatalf("管道执行失败: %v", err)
	}
	if got == nil {
		t.Fatal("next 未被调用")
	}
	return got
}

func fptr(v float64) *float64 { return &v }
func iptr(v int) *int         { return &v }

// --- 用例 ---

// TestSpecOverridesNoConfig spec 无补偿配置时，请求必须原样通过。
func TestSpecOverridesNoConfig(t *testing.T) {
	orig := &ir.UnifiedChatRequest{
		ProviderID:      "fake",
		Instructions:    "客户端自己的系统提示",
		Temperature:     fptr(0.9),
		MaxOutputTokens: iptr(1024),
	}

	for name, content := range map[string]string{
		"无spec":   "",
		"spec无字段": `{"provider_id":"fake","spec_version":"t1","base_url":"https://x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			installSpecStore(t, content)
			got := runThroughMiddleware(t, orig.Clone())
			if got.Instructions != orig.Instructions {
				t.Errorf("Instructions = %q, want %q", got.Instructions, orig.Instructions)
			}
			if got.Temperature == nil || *got.Temperature != 0.9 {
				t.Errorf("Temperature 不应被改动: %v", got.Temperature)
			}
			if got.MaxOutputTokens == nil || *got.MaxOutputTokens != 1024 {
				t.Errorf("MaxOutputTokens 不应被改动: %v", got.MaxOutputTokens)
			}
		})
	}
}

// TestSpecOverridesInjectSystemPrompt 覆盖注入的两种形态：空 / 非空 Instructions。
func TestSpecOverridesInjectSystemPrompt(t *testing.T) {
	installSpecStore(t, fakeSpecJSON)

	t.Run("Instructions为空", func(t *testing.T) {
		got := runThroughMiddleware(t, &ir.UnifiedChatRequest{ProviderID: "fake"})
		if got.Instructions != "渠道注入的系统提示" {
			t.Errorf("Instructions = %q, want 注入值原样顶上", got.Instructions)
		}
	})

	t.Run("Instructions非空", func(t *testing.T) {
		got := runThroughMiddleware(t, &ir.UnifiedChatRequest{
			ProviderID:   "fake",
			Instructions: "客户端系统提示",
		})
		if got.Instructions != "客户端系统提示\n\n渠道注入的系统提示" {
			t.Errorf("Instructions = %q, want 原值+空行+注入值", got.Instructions)
		}
	})

	t.Run("重复应用不翻倍", func(t *testing.T) {
		// 渠道同时被"网关统一接线"和"spec middlewares 数组"声明时，
		// 中间件会在管道里出现两次，注入必须幂等。
		got := runThroughMiddleware(t, &ir.UnifiedChatRequest{
			ProviderID:   "fake",
			Instructions: "客户端系统提示",
		})
		sp := spec.Load("fake")
		if sp == nil {
			t.Fatal("假 spec 未加载")
		}
		applySpecOverrides(got, sp)
		if got.Instructions != "客户端系统提示\n\n渠道注入的系统提示" {
			t.Errorf("二次应用后 Instructions = %q, 注入应幂等", got.Instructions)
		}
	})

	t.Run("空白注入视为未配置", func(t *testing.T) {
		installSpecStore(t, `{"provider_id":"fake","spec_version":"t1","system_prompt_inject":"   "}`)
		got := runThroughMiddleware(t, &ir.UnifiedChatRequest{
			ProviderID:   "fake",
			Instructions: "保持原样",
		})
		if got.Instructions != "保持原样" {
			t.Errorf("空白注入不应生效, got %q", got.Instructions)
		}
	})
}

// TestSpecOverridesSampling 缺省值只补空、不覆盖客户端显式值。
func TestSpecOverridesSampling(t *testing.T) {
	installSpecStore(t, fakeSpecJSON)

	t.Run("客户端已给两者", func(t *testing.T) {
		got := runThroughMiddleware(t, &ir.UnifiedChatRequest{
			ProviderID:      "fake",
			Temperature:     fptr(0.9),
			MaxOutputTokens: iptr(1024),
		})
		if got.Temperature == nil || *got.Temperature != 0.9 {
			t.Errorf("客户端显式 Temperature 不应被覆盖: %v", got.Temperature)
		}
		if got.MaxOutputTokens == nil || *got.MaxOutputTokens != 1024 {
			t.Errorf("客户端显式 MaxOutputTokens 不应被覆盖: %v", got.MaxOutputTokens)
		}
	})

	t.Run("客户端未给", func(t *testing.T) {
		got := runThroughMiddleware(t, &ir.UnifiedChatRequest{ProviderID: "fake"})
		if got.Temperature == nil || *got.Temperature != 0.3 {
			t.Errorf("Temperature 应补 spec 缺省 0.3: %v", got.Temperature)
		}
		if got.MaxOutputTokens == nil || *got.MaxOutputTokens != 4096 {
			t.Errorf("MaxOutputTokens 应补 spec 缺省 4096: %v", got.MaxOutputTokens)
		}
	})

	t.Run("只缺其一", func(t *testing.T) {
		got := runThroughMiddleware(t, &ir.UnifiedChatRequest{
			ProviderID:  "fake",
			Temperature: fptr(0.9),
		})
		if got.Temperature == nil || *got.Temperature != 0.9 {
			t.Errorf("已给的 Temperature 不应被覆盖: %v", got.Temperature)
		}
		if got.MaxOutputTokens == nil || *got.MaxOutputTokens != 4096 {
			t.Errorf("缺失的 MaxOutputTokens 应补 4096: %v", got.MaxOutputTokens)
		}
	})
}

// TestSpecOverridesPassesThrough 空渠道 ID 一律放行。
func TestSpecOverridesPassesThrough(t *testing.T) {
	installSpecStore(t, fakeSpecJSON)
	got := runThroughMiddleware(t, &ir.UnifiedChatRequest{
		Instructions: "无渠道归属",
		Temperature:  fptr(0.9),
	})
	if got.Instructions != "无渠道归属" || *got.Temperature != 0.9 {
		t.Errorf("ProviderID 为空应原样放行, got %+v", got)
	}
}
