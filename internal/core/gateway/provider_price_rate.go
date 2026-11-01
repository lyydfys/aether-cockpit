package gateway

import "fmt"

// 渠道级计费倍率（渠道编辑器 高级 → Price rate）。
//
// 中转站常按官方价打折/加价（0.8×、1.5×）。此前 `priceRate` 只在前后端
// draft 里，`providerDraft` 没有字段，保存被静默丢弃，用量页永远按官方价算。
// 现在存进 settings.ProviderPriceRate，并在两处成本口径生效：
//   - 用量页聚合（handleUsage）：cost = costOf(价格表) × 渠道倍率；
//   - 请求账本（finalize）：r.Cost = costOf(...) × 渠道倍率。
//
// 与 model_prices 的既有口径一致：改倍率会重算历史（用量存 token 不存金额）。
// 未设置 / 等于 1 视为不乘。

// providerPriceRate 返回渠道计费倍率（默认 1）。
func (g *Gateway) providerPriceRate(providerID string) float64 {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if v, ok := g.settings.ProviderPriceRate[providerID]; ok && v >= 0 {
		return v
	}
	return 1
}

// setProviderPriceRate 保存渠道计费倍率：1 或空删除条目（回到官方价）。
func (g *Gateway) setProviderPriceRate(providerID string, rate *float64) error {
	if rate == nil {
		return nil
	}
	v := *rate
	if v < 0 || v > 1000 {
		return fmt.Errorf("Price rate 需在 0 到 1000 之间")
	}
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderPriceRate == nil {
		g.settings.ProviderPriceRate = map[string]float64{}
	}
	if v == 1 {
		delete(g.settings.ProviderPriceRate, providerID)
	} else {
		g.settings.ProviderPriceRate[providerID] = v
	}
	if len(g.settings.ProviderPriceRate) == 0 {
		g.settings.ProviderPriceRate = nil
	}
	return g.saveSettings()
}

// providerPriceRateView 供 /api/providers 回显；未设置返回 0（omitempty 掉）。
func (g *Gateway) providerPriceRateView(providerID string) float64 {
	if v := g.providerPriceRate(providerID); v != 1 {
		return v
	}
	return 0
}
