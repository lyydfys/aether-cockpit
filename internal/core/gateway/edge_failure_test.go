package gateway

import (
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// Cloudflare 5xx 是"线路/边缘"故障，不是账号问题。
//
// 判断错了会连锁作恶：把 520 当成账号失败 → 连续几次就熔断一个好账号
// → 账号池越打越少 → 最后无号可换，请求直接断掉。
func TestIsEdgeFailureRecognizesCloudflare5xx(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"上游故障（520）", true},
		{"上游故障（521）", true},
		{"上游故障（522）", true},
		{"上游故障（523）", true},
		{"上游故障（524）", true},
		{"DeepSeek Messages request failed (520)", true},
		{"cloudflare: origin is unreachable", true},
		{"web server is down", true},
		// 普通账号问题不应被当成线路问题。
		{"上游拒绝凭证（401）：API Key 无效或已过期", false},
		{"上游限流（429）", false},
		{"上游没有这个接口或模型（404）", false},
		{"上游账户余额不足（402）", false},
		{"上游故障（503）", false}, // 503 可能是真上游故障，仍按原逻辑
	}
	for _, c := range cases {
		err := ir.NewAPIError(ir.ErrCodeUpstreamFailed, c.msg)
		if got := isEdgeFailure(err); got != c.want {
			t.Errorf("isEdgeFailure(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestIsEdgeFailureNilSafe(t *testing.T) {
	if isEdgeFailure(nil) {
		t.Fatal("nil 不该被判成线路故障")
	}
}

// 线路故障时不应给账号记失败分（否则会把好号熔断掉）。
func TestEdgeFailureDoesNotPenalizeAccount(t *testing.T) {
	g := New(Config{
		SettingsPath:     t.TempDir() + "/settings.json",
		AccountStorePath: t.TempDir() + "/accounts.json",
		UsagePath:        "-",
		MaxRecords:       10,
	})
	env := provider.NewAccountEnv("acc-edge", "open-design")

	// 连续灌入多次 520：账号不应因此进入熔断/失效态。
	for i := 0; i < 8; i++ {
		g.penalizeModel(env, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游故障（520）"), "")
	}
	if env.State() != provider.StateReady {
		t.Fatalf("线路故障把账号打成 %q（应保持 ready）", env.State())
	}
}

// 账号真出问题时仍要正常惩罚，不能被上面的豁免逻辑误伤。
func TestRealAccountFailureStillPenalizes(t *testing.T) {
	g := New(Config{
		SettingsPath:     t.TempDir() + "/settings.json",
		AccountStorePath: t.TempDir() + "/accounts.json",
		UsagePath:        "-",
		MaxRecords:       10,
	})
	env := provider.NewAccountEnv("acc-real", "open-design")
	for i := 0; i < 8; i++ {
		g.penalizeModel(env, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "上游故障（503）"), "")
	}
	if env.State() == provider.StateReady {
		t.Fatal("真实上游故障反复出现时应触发熔断")
	}
}
