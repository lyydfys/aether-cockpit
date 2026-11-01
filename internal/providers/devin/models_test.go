package devin

import "testing"

func TestResolveDevinChatModelUID(t *testing.T) {
	cases := []struct {
		model  string
		effort string
		want   string
	}{
		{"", "", "swe-2-high"},
		{"swe-2", "", "swe-2-high"},
		{"swe-2", "medium", "swe-2-medium"},
		{"swe-2-high", "", "swe-2-high"},
		{"devin/swe-2", "low", "swe-2-low"},
		{"claude-sonnet-4-5", "", "MODEL_PRIVATE_2"},
		{"claude-sonnet-4-5", "high", "MODEL_PRIVATE_3"},
		{"gpt-5.2", "", "gpt-5-2-low"},
		{"gpt-5.2", "xhigh", "gpt-5-2-xhigh"},
		{"glm-5.2", "none", "glm-5-2-none"},
		{"glm-5.2", "max", "glm-5-2-max"},
		{"claude-sonnet-4-6", "", "claude-sonnet-4-6"},
		{"claude-sonnet-4-6", "high", "claude-sonnet-4-6-thinking"},
		{"glm-5.2:none", "", "glm-5-2-none"},
		{"glm-5.2:high", "", "glm-5-2"},
		{"gemini-3-flash", "", "gemini-3-8-flash-high"},
		{"unknown-model", "", "unknown-model"},
		{"claude-haiku-4-5", "", "MODEL_PRIVATE_11"},
	}
	for _, tc := range cases {
		if got := resolveDevinChatModelUID(tc.model, tc.effort, 0); got != tc.want {
			t.Fatalf("resolve(%q,%q)=%q want %q", tc.model, tc.effort, got, tc.want)
		}
	}
}

func TestNormalizeThinkingLevel(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"off":      "none",
		"disabled": "none",
		"auto":     "high",
		"LOW":      "low",
		"bogus":    "",
	}
	for in, want := range cases {
		if got := normalizeThinkingLevel(in, 0); got != want {
			t.Fatalf("normalize(%q)=%q want %q", in, got, want)
		}
	}
	if got := normalizeThinkingLevel("", 8000); got != "medium" {
		t.Fatalf("预算 8000 应映射 medium，得到 %q", got)
	}
	if got := normalizeThinkingLevel("", 64000); got != "max" {
		t.Fatalf("预算 64000 应映射 max，得到 %q", got)
	}
}
