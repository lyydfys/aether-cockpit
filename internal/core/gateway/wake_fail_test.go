package gateway

import (
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
)

// 唤醒失败要变成"能据此行动"的一句话，而不是把上游 JSON 糊在脸上。
func TestWakeFailTextQuotaExhausted(t *testing.T) {
	ae := ir.NewAPIErrorf(ir.ErrCodeUpstreamRateLimit,
		"Qoder 账号额度已用尽（上游 400：%s）",
		`{"code":"400","message":"[FAIL]node:agent_router msg:None flow nodes found for router agent_router"}`)
	got := wakeFailText(ae)
	if !strings.Contains(got, "额度已用尽") {
		t.Fatalf("应点明额度用尽：%q", got)
	}
	if !strings.Contains(got, "充值或换账号") {
		t.Fatalf("应给出可执行建议：%q", got)
	}
	if len(got) > 260 {
		t.Fatalf("文案过长（%d 字节），会把 toast 撑爆：%q", len(got), got)
	}
}

func TestWakeFailTextCategories(t *testing.T) {
	cases := []struct {
		ae   *ir.APIError
		want string
	}{
		{ir.NewAPIError(ir.ErrCodeUpstreamAuth, "401 invalid token"), "登录态失效"},
		{ir.NewAPIError(ir.ErrCodeUpstreamRateLimit, "429 too many requests"), "上游限流"},
		{ir.NewAPIError(ir.ErrCodeUpstreamFailed, "dial tcp timeout"), "上游不可达"},
		{ir.NewAPIError(ir.ErrCodeBadRequest, "bad payload"), "bad payload"},
		{nil, "未知错误"},
	}
	for _, c := range cases {
		if got := wakeFailText(c.ae); !strings.Contains(got, c.want) {
			t.Errorf("wakeFailText(%v) = %q，want 含 %q", c.ae, got, c.want)
		}
	}
}

// WorkBuddy 的 14018 "Credits exhausted" 也要按额度耗尽处理。
func TestWakeFailTextWorkBuddyCredits(t *testing.T) {
	ae := ir.NewAPIError(ir.ErrCodeUpstreamRateLimit,
		`WorkBuddy 限流：{"error":{"data":{"code":14018,"msg":"Credits exhausted. Please visit the link below to purchase add-on packs"}}}`)
	got := wakeFailText(ae)
	if !strings.Contains(got, "额度已用尽") {
		t.Fatalf("Credits exhausted 应判为额度耗尽：%q", got)
	}
}

func TestBrief(t *testing.T) {
	if got := brief("a\nb\nc", 100); got != "a b c" {
		t.Fatalf("应压平换行：%q", got)
	}
	long := strings.Repeat("x", 200)
	if got := brief(long, 20); len(got) != 23 || !strings.HasSuffix(got, "…") {
		t.Fatalf("截断不对：len=%d %q", len(got), got)
	}
	if got := brief("  ", 10); got != "" {
		t.Fatalf("空白应归一成空串：%q", got)
	}
}
