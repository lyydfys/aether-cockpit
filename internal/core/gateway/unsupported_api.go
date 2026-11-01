package gateway

import "net/http"

// 本文件放"前端会调、但本产品没有对应真实动作"的端点。
//
// 原则（沿用 GAP §2169/§2355 的裁定）：**不做假实现**。要么给出真实语义，
// 要么显式 501/409 + 可执行的原因，绝不返回一个形状像成功、语义为空的响应。

// handleProviderMove POST /api/provider/move
//
// magpie 的语义是"把内置订阅迁到它自己的 npm 插件包"，前端三处调用点
// （app.js:6476 / app.js:14923 / plugins.js:370）都把返回值当**完整 providers
// 视图**消费。我们不做 JS/Bun 插件运行时（单 exe / 零依赖的产品约束，属于
// 项目主动裁剪），所以没有可迁移的目标，也没有可对应的真实动作。
//
// 可达性说明：这三处调用都挂在 `p.move` / `plugins.movable` / `unmoved` 上，
// 而我们的 providers 视图不下发这些字段（plugins/unmoved 恒为空数组），
// 因此当前界面不会触发它；保留 501 是为了让"按 magpie 契约直接调 API"的
// 调用方拿到明确原因，而不是 404。
func (g *Gateway) handleProviderMove(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error": "Aether Cockpit 没有插件包模型，无法把内置订阅迁到插件：渠道由内核代码注册，" +
			"账号保存在账号池里；如需更换协议/端点，用渠道编辑器的自定义供应商或 models 映射",
		"code": "no_plugin_packages",
	})
}

// handleProviderUnfetch POST /api/provider/unfetch
//
// 前端在"Fetch models"之后给 `p.fetched` 的渠道显示 Forget 按钮，调用它丢弃
// vendor 模型清单。我们的模型清单来自渠道 Manifest / spec（服务端不持久化
// 抓取结果），所以**没有可丢弃的状态**：返回当前完整 providers 视图是准确的
// 幂等结果，不是"假装删掉"。
//
// 可达性：providers 视图不下发 `fetched`，所以按钮当前不出现；保留端点是为了
// 契约完整（调用方拿到 200 + 最新视图，而不是 404）。
func (g *Gateway) handleProviderUnfetch(w http.ResponseWriter, r *http.Request) {
	g.respondProviders(w, r)
}
