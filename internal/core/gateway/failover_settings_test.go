package gateway

import (
	"path/filepath"
	"testing"
	"time"
)

// 故障转移旋钮：设置能真正改到运行中的网关，且越界值被夹住。
func TestApplyFailoverSettings(t *testing.T) {
	g := New(Config{
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		UsagePath:        "-",
		MaxRecords:       10,
	})
	if g.cfg.FirstByteTimeout != 60*time.Second {
		t.Fatalf("默认首字节超时应为 60s，得到 %v", g.cfg.FirstByteTimeout)
	}
	if g.cfg.MaxAttempts != 5 {
		t.Fatalf("默认重试次数应为 5，得到 %d", g.cfg.MaxAttempts)
	}

	// 正常范围：生效。
	g.settingsLockedSet(func() {
		g.settings.FirstByteTimeoutSec = 20
		g.settings.MaxAttempts = 8
	})
	g.applyFailoverSettings()
	if g.cfg.FirstByteTimeout != 20*time.Second {
		t.Fatalf("首字节超时 = %v，want 20s", g.cfg.FirstByteTimeout)
	}
	if g.cfg.MaxAttempts != 8 {
		t.Fatalf("重试次数 = %d，want 8", g.cfg.MaxAttempts)
	}

	// 越界：忽略（保持上一次有效值），不能把超时设成 1 秒。
	g.settingsLockedSet(func() {
		g.settings.FirstByteTimeoutSec = 1
		g.settings.MaxAttempts = 99
	})
	g.applyFailoverSettings()
	if g.cfg.FirstByteTimeout != 20*time.Second {
		t.Fatalf("越界超时应被忽略，得到 %v", g.cfg.FirstByteTimeout)
	}
	if g.cfg.MaxAttempts != 8 {
		t.Fatalf("越界重试次数应被忽略，得到 %d", g.cfg.MaxAttempts)
	}

	// 零值：保持默认，不覆盖。
	g.settingsLockedSet(func() {
		g.settings.FirstByteTimeoutSec = 0
		g.settings.MaxAttempts = 0
	})
	g.applyFailoverSettings()
	if g.cfg.FirstByteTimeout != 20*time.Second || g.cfg.MaxAttempts != 8 {
		t.Fatalf("零值不该覆盖已有配置：%v / %d", g.cfg.FirstByteTimeout, g.cfg.MaxAttempts)
	}
}

// settingsLockedSet 用最小接口改设置（测试辅助）。
func (g *Gateway) settingsLockedSet(fn func()) {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	fn()
}
