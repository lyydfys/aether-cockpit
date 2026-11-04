package openaicompat

import (
	"testing"

	"aether-cockpit/internal/core/spec"
)

// 通用 spec 回退只能继承行为，不能继承地址与模型映射。
//
// 这条曾经出过真实事故：所有没有自己 spec 的 OpenAI 兼容渠道（包括用户
// 新加的）整份继承了 configs/specs/openai-compat.json，而那个示例文件里
// 写着 base_url: https://api.deepseek.com/v1 —— 用户的渠道密钥被发到了
// DeepSeek 的地址上，表现为 401 "api key invalid"。
func TestGenericSpecForVendorDropsAddressAndMapping(t *testing.T) {
	gen := &spec.Spec{
		ProviderID:         "openai-compat",
		BaseURL:            "https://api.deepseek.com/v1",
		ClientVersion:      "1.0.0",
		HeadersTemplate:    map[string]string{"X-Device-Id": "{{.Fingerprint.DeviceID}}"},
		Models:             map[string]string{"claude-4-sonnet": "deepseek-chat"},
		Endpoints:          map[string]string{"chat": "/chat/completions"},
		Middlewares:        []string{"xml_tool_call"},
		SystemPromptInject: "示例提示词",
	}
	got := genericSpecForVendor(gen)
	if got == nil {
		t.Fatal("不应返回 nil")
	}
	// 必须清掉的：地址、模型映射、系统提示词、以及被清空的身份。
	if got.BaseURL != "" {
		t.Errorf("base_url 必须清掉（否则请求被发到示例地址），得到 %q", got.BaseURL)
	}
	if got.Models != nil {
		t.Errorf("模型映射必须清掉（否则示例映射会改掉真实模型名），得到 %v", got.Models)
	}
	if got.SystemPromptInject != "" {
		t.Errorf("系统提示词注入必须清掉，得到 %q", got.SystemPromptInject)
	}
	if got.ProviderID != "" {
		t.Errorf("继承副本不应带着 openai-compat 的身份，得到 %q", got.ProviderID)
	}
	// 应当保留的：怎么说话。
	if got.HeadersTemplate["X-Device-Id"] == "" {
		t.Error("头模板应当保留")
	}
	if got.Endpoint("chat", "") != "/chat/completions" {
		t.Errorf("端点路径应当保留，得到 %q", got.Endpoint("chat", ""))
	}
	if len(got.Middlewares) != 1 || got.Middlewares[0] != "xml_tool_call" {
		t.Errorf("补偿中间件应当保留，得到 %v", got.Middlewares)
	}
	// 不修改原对象（热更新共享同一份 spec，改了会污染其它渠道）。
	if gen.BaseURL == "" || gen.Models == nil {
		t.Error("不得修改传入的通用 spec 本体")
	}
	if genericSpecForVendor(nil) != nil {
		t.Error("nil 输入应返回 nil")
	}
}
