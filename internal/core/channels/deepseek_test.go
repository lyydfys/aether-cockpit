package channels

import "testing"

// DeepSeek 渠道的模型清单必须跟官方 /v1/models 对齐（2026-11-06 实测只有两个）：
//   - deepseek-flash（DeepSeek-V4.1-Flash）：1M 上下文、支持图片输入
//   - deepseek-v4-pro（DeepSeek-V4-Pro）：1M 上下文、纯文本
//
// 旧的 deepseek-chat / deepseek-reasoner 已不在官方列表里，界面不该再展示，
// 否则用户选了之后会拿到上游 404/400。
func TestDeepSeekVendorModels(t *testing.T) {
	var ds *Vendor
	for i := range vendors {
		if vendors[i].ID == "deepseek" {
			ds = &vendors[i]
			break
		}
	}
	if ds == nil {
		t.Fatal("缺少 deepseek 渠道")
	}
	if ds.BaseURL == "" || ds.KeysURL == "" {
		t.Fatalf("deepseek 渠道地址不完整：%+v", ds)
	}

	want := map[string]struct {
		name    string
		context int
		vision  bool
	}{
		"deepseek-flash":  {"DeepSeek V4.1 Flash", 1048576, true},
		"deepseek-v4-pro": {"DeepSeek V4 Pro", 1048576, false},
	}
	if len(ds.Models) != len(want) {
		t.Fatalf("模型数量 = %d，want %d（%v）", len(ds.Models), len(want), ds.Models)
	}
	for _, m := range ds.Models {
		w, ok := want[m.UpstreamModel]
		if !ok {
			t.Fatalf("出现了未登记的模型：%s（%s）", m.UpstreamModel, m.ID)
		}
		if m.ID != "deepseek/"+m.UpstreamModel {
			t.Errorf("%s: 模型 ID 前缀不对：%q", m.UpstreamModel, m.ID)
		}
		if m.DisplayName != w.name {
			t.Errorf("%s: 显示名 = %q，want %q", m.UpstreamModel, m.DisplayName, w.name)
		}
		if m.ContextWindow != w.context {
			t.Errorf("%s: 上下文 = %d，want %d", m.UpstreamModel, m.ContextWindow, w.context)
		}
		if !m.Capabilities.ToolCall || !m.Capabilities.Streaming {
			t.Errorf("%s: 缺少 tool/stream 能力：%+v", m.UpstreamModel, m.Capabilities)
		}
		if m.Capabilities.Vision != w.vision {
			t.Errorf("%s: Vision = %v，want %v", m.UpstreamModel, m.Capabilities.Vision, w.vision)
		}
	}
}
