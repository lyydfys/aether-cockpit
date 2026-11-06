package reverse

import (
	"strings"
	"testing"
)

// UA 必须按渠道生态选：给 OpenAI 兼容渠道套 Codex 的 UA，和没有 UA 一样可疑。
func TestClientUAForEcosystem(t *testing.T) {
	cases := []struct {
		profile string
		version string
		prefix  string
	}{
		{"opencode-go", "1.19.5", "opencode/1.19.5"},
		{"opencode", "1.19.5", "opencode/1.19.5"},
		{"codex", "0.16.0", "codex_cli_rs/0.16.0 ("},
		{"github-copilot", "0.26.7", "GitHubCopilotChat/0.26.7"},
		{"zcode", "1.0", "openai-node/1.0"},
		{"zcode-cn", "1.0", "openai-node/1.0"},
	}
	for _, c := range cases {
		got := clientUAFor(c.profile, c.version)
		if !strings.HasPrefix(got, c.prefix) {
			t.Errorf("clientUAFor(%q, %q) = %q，want 前缀 %q", c.profile, c.version, got, c.prefix)
		}
		// 任何分支都不许出现本产品品牌。
		if strings.Contains(strings.ToLower(got), "aether") {
			t.Errorf("clientUAFor(%q) 泄漏品牌：%q", c.profile, got)
		}
	}
	// 未给版本时用兜底版本，且仍带生态前缀。
	if got := clientUAFor("zcode", ""); !strings.HasPrefix(got, "openai-node/"+reverseClientVersion) {
		t.Errorf("缺省版本兜底不对：%q", got)
	}
}
