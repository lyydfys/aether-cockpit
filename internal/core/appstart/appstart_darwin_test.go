//go:build darwin

package appstart

import (
	"strings"
	"testing"
)

func TestLaunchAgentPlist(t *testing.T) {
	got := launchAgentPlist(`/Applications/Aether Cockpit.app/Contents/MacOS/AetherCockpit`)
	for _, want := range []string{
		"<string>com.aether.cockpit</string>",
		`<string>/Applications/Aether Cockpit.app/Contents/MacOS/AetherCockpit</string>`,
		"<key>RunAtLoad</key>",
		"<true/>",
		"<string>Aqua</string>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("plist 缺少 %q:\n%s", want, got)
		}
	}
}

func TestLaunchAgentEscapesPath(t *testing.T) {
	got := launchAgentPlist(`/tmp/a&b.app/Aether.exe`)
	if !strings.Contains(got, `a&amp;b.app`) {
		t.Fatalf("路径未 XML 转义: %s", got)
	}
}
