package openaicompat

import (
	"net/http"
	"testing"
)

// 自定义渠道的附加头：不覆盖渠道代码已设置的头；值里的 {{api_key}} 会被替换。
func TestApplyConfigHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Keep", "orig")
	h.Set("Authorization", "Bearer sk-code")

	applyConfigHeaders(h, map[string]string{
		"X-Keep":     "should-not-override",
		"X-Tenant":   "t1",
		"X-Key-Echo": "{{api_key}}",
	}, "sk-123")

	if got := h.Get("X-Keep"); got != "orig" {
		t.Fatalf("渠道代码设置的头不应被覆盖，got %q", got)
	}
	if got := h.Get("X-Tenant"); got != "t1" {
		t.Fatalf("X-Tenant = %q", got)
	}
	if got := h.Get("X-Key-Echo"); got != "sk-123" {
		t.Fatalf("{{api_key}} 未替换: %q", got)
	}
	if got := h.Get("Authorization"); got != "Bearer sk-code" {
		t.Fatalf("Authorization = %q", got)
	}
}
