package gateway

import "testing"

// 反代渠道要有默认 TLS 指纹，官方 API Key 渠道要保持默认（不套浏览器指纹）。
func TestDefaultTLSProfile(t *testing.T) {
	cases := map[string]string{
		"workbuddy-cn":   "chrome_131",
		"qoder-intl":     "chrome_131",
		"claude":         "chrome_131",
		"opencode-go":    "chrome_131",
		"openai":         "", // 官方 API：不设默认
		"deepseek":       "",
		"not-exist-prov": "",
	}
	for id, want := range cases {
		if got := defaultTLSProfile(id); got != want {
			t.Errorf("defaultTLSProfile(%q) = %q, want %q", id, got, want)
		}
	}
}
