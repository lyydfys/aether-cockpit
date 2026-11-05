package reverse

import (
	"net/url"
	"strings"
	"testing"
)

// OpenCode Go 的默认端点必须是 canonical 路径。
//
// 背景（2026-11-05 实测）：OpenCode Go 背后是"原生官转透传 + 翻译层官转 +
// 自部署 cell"的动态混合池，路径决定命中率：
//
//	/inference/go/openai/v1  ≈ 80%
//	/zen/go/v1               6/8（小样本）
//	/inference/go/v1         3/8（DSH 的 pi-ai 默认，最差）
//
// 另外旧 /zen/go 要 x-api-key，而本引擎默认发 Bearer；canonical 接受 Bearer。
// 这两条任何一条退化，都会让"尽量走官方"变成空话，所以用测试钉住。
func TestOpencodeGoUsesCanonicalEndpoint(t *testing.T) {
	p := findProfile(t, "opencode-go")

	if got, want := p.DefaultBaseURL, "https://opencode.ai/inference/go/openai/v1"; got != want {
		t.Fatalf("opencode-go 默认端点 = %q，want %q", got, want)
	}
	if strings.Contains(p.DefaultBaseURL, "/zen/") {
		t.Fatal("不该再用旧的 /zen 路径：官方命中率更低且鉴权头不一致")
	}
	// 拼出来的实际请求地址必须是 canonical chat completions。
	full := strings.TrimRight(p.DefaultBaseURL, "/") + p.DefaultChatPath
	u, err := url.Parse(full)
	if err != nil {
		t.Fatalf("端点无法解析：%v", err)
	}
	if got, want := u.Path, "/inference/go/openai/v1/chat/completions"; got != want {
		t.Fatalf("chat 路径 = %q，want %q", got, want)
	}
	// session 头是这个渠道的硬要求（缺了会 400 MissingSessionID）。
	if p.SessionHeader != "x-opencode-session" {
		t.Fatalf("session 头 = %q", p.SessionHeader)
	}
	// 模型目录不在 canonical chat 前缀下：/inference/go/openai/v1/models 会 404。
	// 目录必须用可用的绝对 URL，否则渠道会"没有模型"（实测刷新失败）。
	if !strings.HasPrefix(p.ModelListPath, "https://opencode.ai/") {
		t.Fatalf("模型目录应走绝对 URL，得到 %q", p.ModelListPath)
	}
	if strings.Contains(p.ModelListPath, "/openai/v1/models") {
		t.Fatalf("该路径实测 404，不该用：%q", p.ModelListPath)
	}
}

// Zen（按量付费）与 Go（订阅）是两条独立端点，别互相串。
func TestOpencodeZenEndpointUnchanged(t *testing.T) {
	p := findProfile(t, "opencode")
	if got, want := p.DefaultBaseURL, "https://opencode.ai/zen/v1"; got != want {
		t.Fatalf("opencode(Zen) 端点 = %q，want %q", got, want)
	}
}
