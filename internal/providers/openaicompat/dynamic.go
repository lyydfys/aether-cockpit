package openaicompat

import "aether-cockpit/internal/provider"

// RegisterDynamic 注册一个"运行时才有"的渠道（用户自定义渠道）。
//
// 与 channels 里的内置预设走同一条实现路径，区别只在配置来自设置而不是
// 编译期常量。重复注册同 ID 时替换而不是 panic —— 用户改完渠道点保存，
// 会先注册新配置再让旧对象自然失效。
//
// 安全边界（重要）：这里只接受"OpenAI/Anthropic/Responses 兼容端点"这几个
// 字段，不接受可执行钩子或脚本。自定义渠道是配置，不是插件。
func RegisterDynamic(cfg Config) {
	if cfg.ID == "" {
		return
	}
	provider.RegisterOrReplace(&Provider{cfg: cfg})
}

// UnregisterDynamic 下线一个自定义渠道。
func UnregisterDynamic(id string) bool {
	return provider.Unregister(id)
}
