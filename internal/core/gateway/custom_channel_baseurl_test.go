package gateway

import (
	"strings"
	"testing"
)

// 只给域名时自动补 /v1。
//
// 用户填 "https://ai.example.com" 时，网关会拼成 /chat/completions，
// 中转站把该路径先前端路由，返回单页应用 HTML（HTTP 200），
// 于是唤醒/调用全部失败，报错还像是"上游不可达"。
func TestNormalizeCustomChannelAppendsV1(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://ai.example.com", "https://ai.example.com/v1"},
		{"https://ai.example.com/", "https://ai.example.com/v1"},
		{"https://ai.example.com  ", "https://ai.example.com/v1"},
		// 已经写了路径的一律不动。
		{"https://ai.example.com/v1", "https://ai.example.com/v1"},
		{"https://ai.example.com/api/v3", "https://ai.example.com/api/v3"},
		{"https://ai.example.com/openai/v1", "https://ai.example.com/openai/v1"},
	}
	for _, c := range cases {
		ch, err := normalizeCustomChannel(CustomChannel{
			ID: "mca", Name: "MCA", BaseURL: c.in, Protocol: "openai",
		})
		if err != nil {
			t.Fatalf("%q: 归一化失败: %v", c.in, err)
		}
		if ch.BaseURL != c.want {
			t.Errorf("%q -> %q，期望 %q", c.in, ch.BaseURL, c.want)
		}
	}
}

// 带端口的域名同样要能补。
func TestNormalizeCustomChannelAppendsV1WithPort(t *testing.T) {
	ch, err := normalizeCustomChannel(CustomChannel{
		ID: "local", Name: "Local", BaseURL: "http://127.0.0.1:3000", Protocol: "openai",
	})
	if err != nil {
		t.Fatalf("归一化失败: %v", err)
	}
	if !strings.HasSuffix(ch.BaseURL, ":3000/v1") {
		t.Fatalf("带端口应补成 :3000/v1，实际 %q", ch.BaseURL)
	}
}
