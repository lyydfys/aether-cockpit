package mobileproviders

import (
	"testing"

	"aether-cockpit/internal/provider"
)

// The mobile bridge must register the same channel families as the desktop
// entry point; otherwise a real .aether-accounts bundle fails with
// "渠道未注册" for accounts that are valid on Windows.
func TestProviderMatrixCoversDesktopChannels(t *testing.T) {
	required := []string{
		"antigravity", "antigravity-ide", "claude-sub",
		"codebuddy-cn", "codebuddy-intl", "cursor", "devin",
		"gemini-compat", "github-copilot", "kiro",
		"minimax", "minimax-cn", "minimax-code", "minimax-code-global",
		"openai-compat", "codex", "opencode",
		"qoder-cn", "qoder-intl",
		"trae-cn", "trae-intl", "trae-solo", "trae-solo-cn",
		"typesafe", "vertex", "windsurf",
		"workbuddy-cn", "workbuddy-intl",
		"zcode", "zcode-cn", "zed",
	}
	for _, id := range required {
		if _, ok := provider.Get(id); !ok {
			t.Errorf("mobile provider matrix is missing %q", id)
		}
	}
	if got := len(provider.IDs()); got < 60 {
		t.Errorf("registered providers = %d, want >= 60 (desktop parity)", got)
	}
}
