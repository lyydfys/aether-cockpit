package gateway

import "testing"

func TestHideEmptyVendorRow(t *testing.T) {
	cases := []struct {
		id       string
		accounts int
		want     bool
	}{
		{"deepseek", 0, true},
		{"qwen-cn", 0, true},
		{"cline", 0, true},
		{"deepseek", 1, false},
		{"openai-compat", 0, false},
		{"claude-sub", 0, false},
		{"gemini-compat", 0, false},
		{"google", 0, false},
		{"mock-native", 0, false},
	}
	for _, c := range cases {
		if got := hideEmptyVendorRow(c.id, c.accounts); got != c.want {
			t.Errorf("hideEmptyVendorRow(%q,%d) = %v, want %v", c.id, c.accounts, got, c.want)
		}
	}
}
