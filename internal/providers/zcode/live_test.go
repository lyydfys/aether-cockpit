package zcode

import (
	"context"
	"os"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// TestLiveZCodeChat 用本机真实 ZCode 登录态发一次最小请求。
// 默认跳过；ZCODE_LIVE=1 才跑（会消耗少量 Coding Plan 配额）。
func TestLiveZCodeChat(t *testing.T) {
	if os.Getenv("ZCODE_LIVE") != "1" {
		t.Skip("设置 ZCODE_LIVE=1 才跑真实 Coding Plan 请求")
	}
	cases := []*Provider{
		newProvider("zcode-cn", "cn", "https://open.bigmodel.cn/api/coding/paas/v4", "BigModel"),
		newProvider("zcode", "intl", "https://api.z.ai/api/coding/paas/v4", "Z.ai"),
	}
	for _, p := range cases {
		drafts, err := p.DiscoverLocalAccounts(context.Background())
		if err != nil {
			t.Logf("%s: 本机无登录态（%v）", p.id, err)
			continue
		}
		env := provider.NewAccountEnv("live-"+p.id, p.id)
		env.Credentials = drafts[0].Credentials
		ch, err := p.ChatStream(context.Background(), env, &ir.UnifiedChatRequest{
			Model:         p.id + "/glm-5.3",
			UpstreamModel: "glm-5.3",
			Items:         []ir.UnifiedItem{{Type: ir.ItemTypeMessage, Role: "user", Text: "只回复 OK"}},
		})
		if err != nil {
			msg := err.Error()
			if strings.Contains(msg, "1113") || strings.Contains(msg, "余额不足") {
				t.Logf("%s: 账号侧无可用资源包（%v），跳过", p.id, err)
				continue
			}
			t.Errorf("%s: ChatStream: %v", p.id, err)
			continue
		}
		var text strings.Builder
		var done bool
		for ev := range ch {
			text.WriteString(ev.TextDelta)
			if ev.Type == ir.EventDone {
				done = true
			}
		}
		t.Logf("%s: bytes=%d done=%v", p.id, text.Len(), done)
		if text.Len() == 0 && !done {
			t.Errorf("%s: 没有收到任何文本或结束事件", p.id)
		}
	}
}
