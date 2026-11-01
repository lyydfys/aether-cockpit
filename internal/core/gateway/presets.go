package gateway

import "aether-cockpit/internal/core/channels"

// vendorPresetIDs 是「预设厂商」的集合：这些渠道没有账号时只出现在
// 「添加供应商」弹层，不占用供应商页（与 magpie 只列已配置供应商一致）。
var vendorPresetIDs = func() map[string]bool {
	out := map[string]bool{}
	for _, v := range channels.Vendors() {
		out[v.ID] = true
	}
	return out
}()

// hideEmptyVendorRow 报告某渠道行是否应因「零账号的预设厂商」而隐藏。
func hideEmptyVendorRow(id string, accounts int) bool {
	return accounts == 0 && vendorPresetIDs[id]
}

// extraPresets 是 channels（OpenAI 兼容厂商）之外的订阅 / 原生协议预设：
// Anthropic 既接受 sk-ant-api 按量 Key，也接受订阅 OAuth Token；
// Google 走 gemini-compat 的 /v1beta。
func extraPresets() []map[string]any {
	return []map[string]any{
		{
			"id": "claude-sub", "name": "Anthropic", "icon": "claude-color", "kind": "vendor",
			"anthropic": "https://api.anthropic.com",
			"website":   "https://console.anthropic.com",
			"keysUrl":   "https://console.anthropic.com/settings/keys",
			"note":      "API Key 或订阅 Token",
		},
		{
			"id": "vertex", "name": "Google Vertex AI", "icon": "vertexai-color", "kind": "vendor",
			"vertex":  true,
			"website": "https://cloud.google.com/vertex-ai",
			"keysUrl": "https://console.cloud.google.com/apis/credentials",
			"note":    "你的 Google Cloud 项目",
		},
		{
			"id": "azure", "name": "Azure OpenAI", "icon": "azure", "kind": "vendor",
			"endpoint":       "https://myresource.openai.azure.com",
			"endpointHint":   "你的资源 Endpoint；部署名作为模型（azure/<deployment>）",
			"endpointNeeded": "需要填写资源的 Endpoint",
			"website":        "https://portal.azure.com",
			"keysUrl":        "https://portal.azure.com/#view/Microsoft_Azure_ProjectOxford/CognitiveServicesHub/~/OpenAI",
			"note":           "你的 Azure 资源",
		},
		{
			"id": "typesafe", "name": "TypeSafe Jev", "icon": "typesafe", "kind": "vendor",
			"decide":  "https://api.typesafe.ai",
			"website": "https://typesafe.ai",
			"keysUrl": "https://console.typesafe.ai/keys",
			"note":    "决策 API（System One）",
		},
		{
			"id": "google", "name": "Google Gemini", "icon": "gemini-color", "kind": "vendor",
			"chat":    "https://generativelanguage.googleapis.com",
			"website": "https://aistudio.google.com",
			"keysUrl": "https://aistudio.google.com/apikey",
			"note":    "Gemini Developer API",
		},
	}
}

// allPresets 合并厂商目录与额外预设，供 /api/providers 的 presets 字段使用。
func allPresets() []any {
	vendors := channels.Presets()
	out := make([]any, 0, len(vendors)+2)
	for _, p := range vendors {
		out = append(out, p)
	}
	for _, p := range extraPresets() {
		out = append(out, p)
	}
	return out
}
