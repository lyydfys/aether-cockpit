package claude

import (
	"testing"

	"aether-cockpit/internal/provider"
)

// TestRegisteredViaInit 渠道通过 init() 自注册，注册表能按 ID 查到。
// 正式的匿名 import 由 cmd 入口装配（本测试只验证包内 init 生效）。
func TestRegisteredViaInit(t *testing.T) {
	p, ok := provider.Get(channelID)
	if !ok {
		t.Fatal("claude-sub 未注册")
	}
	if _, is := p.(*Provider); !is {
		t.Fatalf("注册的实现类型不符: %T", p)
	}
}

// TestManifest 渠道自描述契约。
func TestManifest(t *testing.T) {
	m := (&Provider{}).Manifest()

	if m.ID != channelID || m.DisplayName != "Claude 订阅" {
		t.Fatalf("id=%q name=%q", m.ID, m.DisplayName)
	}
	// 订阅端点只在海外，必须走代理优先。
	if m.NetworkPolicy != provider.NetPolicyProxyFirst {
		t.Fatalf("network_policy = %q", m.NetworkPolicy)
	}

	auths := map[provider.AuthMethod]bool{}
	for _, a := range m.SupportedAuths {
		auths[a] = true
	}
	if !auths[provider.AuthLocalImport] || !auths[provider.AuthManualInput] {
		t.Fatalf("supported_auths = %+v", m.SupportedAuths)
	}

	if !m.Capabilities.Chat || !m.Capabilities.LocalImport || !m.Capabilities.Refresh {
		t.Fatalf("capabilities = %+v", m.Capabilities)
	}

	// 手动登录表单：access_token 与 session_key 二选一（sessionKey 引导
	// 授权时无需手动抓 token），因此 access_token 不再强制必填。
	var tokenField, sessionKeyField *provider.FormFieldSchema
	for i, f := range m.AuthFormSchema {
		switch f.Key {
		case "access_token":
			tokenField = &m.AuthFormSchema[i]
		case "session_key":
			sessionKeyField = &m.AuthFormSchema[i]
		}
	}
	if tokenField == nil || tokenField.Required {
		t.Fatalf("auth_form_schema = %+v", m.AuthFormSchema)
	}
	if sessionKeyField == nil || sessionKeyField.Type != "password" || sessionKeyField.Required {
		t.Fatalf("session_key 字段 = %+v", sessionKeyField)
	}

	// 模型清单：三个系列 + thinking 变体，全部 200k 上下文。
	if len(m.Models) < 6 {
		t.Fatalf("models = %d", len(m.Models))
	}
	want := map[string]bool{
		"claude-sub/claude-opus-4-6":            false,
		"claude-sub/claude-opus-4-6-thinking":   false,
		"claude-sub/claude-sonnet-4-6":          false,
		"claude-sub/claude-sonnet-4-6-thinking": false,
		"claude-sub/claude-haiku-4-5":           false,
		"claude-sub/claude-haiku-4-5-thinking":  false,
	}
	for _, model := range m.Models {
		if _, ok := want[model.ID]; !ok {
			continue
		}
		want[model.ID] = true
		if model.ContextWindow != 200000 {
			t.Errorf("%s context_window = %d", model.ID, model.ContextWindow)
		}
		if !model.Capabilities.ToolCall || !model.Capabilities.Reasoning || !model.Capabilities.Streaming {
			t.Errorf("%s capabilities = %+v", model.ID, model.Capabilities)
		}
		if model.UpstreamModel == "" {
			t.Errorf("%s 缺少 upstream_model", model.ID)
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("缺少模型 %s", id)
		}
	}
}
