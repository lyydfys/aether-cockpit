package gateway

import (
	"fmt"
	"strings"
)

// 渠道级模型目录模式，对齐 GoModel 的三态配置：
//
//   - merge（默认）：上游实时目录 + 本地静态清单，live 优先、静态补缺。
//     默认选它是为了保留「上游 /models 漏报时模型仍然可路由」的修复；
//     切到 fallback 会重新出现 live 整体覆盖静态的行为。
//   - fallback：上游实时目录优先；拉取失败或为空时才用静态清单。
//   - allowlist：只用静态清单，完全不访问上游 /models（也不自动刷新）。
//
// 模式只影响「目录里有哪些模型」；渠道级的模型勾选（ProviderModels）仍是
// 独立的可见性过滤，二者可以组合使用。
const (
	ModelModeFallback  = "fallback"
	ModelModeAllowlist = "allowlist"
	ModelModeMerge     = "merge"

	// ModelModeDefault 保持既有行为：live + 静态补缺。
	ModelModeDefault = ModelModeMerge
)

// normalizeModelMode 归一化用户输入；空串视为默认模式。
func normalizeModelMode(mode string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModelModeMerge:
		return ModelModeMerge, true
	case ModelModeFallback:
		return ModelModeFallback, true
	case ModelModeAllowlist:
		return ModelModeAllowlist, true
	}
	return "", false
}

// providerModelMode 返回渠道当前的目录模式（未配置 = merge）。
func (g *Gateway) providerModelMode(pid string) string {
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderModelMode == nil {
		return ModelModeDefault
	}
	if mode, ok := g.settings.ProviderModelMode[pid]; ok {
		if norm, valid := normalizeModelMode(mode); valid {
			return norm
		}
	}
	return ModelModeDefault
}

// setProviderModelMode 保存渠道目录模式；默认值不落盘，保持 settings 干净。
func (g *Gateway) setProviderModelMode(pid, mode string) error {
	norm, ok := normalizeModelMode(mode)
	if !ok {
		return fmt.Errorf("未知模型目录模式 %q（可选 fallback / allowlist / merge）", mode)
	}
	g.settingsMu.Lock()
	defer g.settingsMu.Unlock()
	if g.settings.ProviderModelMode == nil {
		g.settings.ProviderModelMode = map[string]string{}
	}
	if norm == ModelModeDefault {
		delete(g.settings.ProviderModelMode, pid)
	} else {
		g.settings.ProviderModelMode[pid] = norm
	}
	if len(g.settings.ProviderModelMode) == 0 {
		g.settings.ProviderModelMode = nil
	}
	return g.saveSettings()
}
