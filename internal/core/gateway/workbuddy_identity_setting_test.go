package gateway

import (
	"testing"

	"aether-cockpit/internal/providers/workbuddy"
)

// 配置项 workbuddy_identity 必须能真正作用到 WorkBuddy 渠道；空值不覆盖默认。
func TestWorkBuddyIdentityFromSettings(t *testing.T) {
	t.Cleanup(func() {
		if err := workbuddy.SetIdentityPreset("desktop"); err != nil {
			t.Fatalf("恢复默认档位失败: %v", err)
		}
	})

	g := &Gateway{}
	g.settings.WorkBuddyIdentity = "quiet"
	g.applyWorkBuddyIdentity()
	if got := workbuddy.IdentityPresetName(); got != "quiet" {
		t.Fatalf("配置 quiet 后实际档位 = %q", got)
	}

	g.settings.WorkBuddyIdentity = "cli"
	g.applyWorkBuddyIdentity()
	if got := workbuddy.IdentityPresetName(); got != "cli" {
		t.Fatalf("配置 cli 后实际档位 = %q", got)
	}

	// 非法值：记警告并保持上一档，不 panic、不静默改成别的档。
	g.settings.WorkBuddyIdentity = "bogus"
	g.applyWorkBuddyIdentity()
	if got := workbuddy.IdentityPresetName(); got != "cli" {
		t.Fatalf("非法值不应改变档位，got %q", got)
	}

	// 空值：不覆盖（环境变量/默认值继续生效）。
	g.settings.WorkBuddyIdentity = ""
	g.applyWorkBuddyIdentity()
}
