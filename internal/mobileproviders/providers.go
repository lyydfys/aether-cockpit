// Package mobileproviders registers the channel implementations available to
// the mobile bridges. Keeping the matrix explicit prevents a channel that works
// on desktop from silently missing on a phone.
//
// 两条**刻意的**例外（不要"补"进来）：
//
//  1. `internal/providers/mock`（mock-native / mock-weak / mock-responses）
//     在桌面端也**不在正式构建里**：它只被 `cmd/cockpit/devmock_on.go` 以
//     `//go:build dev_mock` 引入，`devmock_off.go` 的 `registerDevMock()` 是空操作
//     （原注释：「产品运行时不存在 mock 渠道」）。手机端若 import 它，渠道列表里会
//     凭空出现三个「Mock …」假渠道，既不 1:1、又误导用户，所以这里不注册。
//
//  2. **spec 声明的动态渠道**不需要 import：`internal/mobile/runtime.go` 构建
//     `gateway.Config` 时已设 `SpecDir: paths.SpecDir`，网关启动会在
//     `internal/core/gateway/gateway.go` 里 `spec.Default().LoadDir(cfg.SpecDir)`
//     再 `provider.ReconcileSpecChannels()`。目录不存在时 `LoadDir` 直接返回 nil
//     （`internal/core/spec/spec.go` 的 os.IsNotExist 分支），所以手机上"没有 spec 目录"
//     是安静的 no-op：不报错、不阻塞启动。
package mobileproviders

import (
	_ "aether-cockpit/internal/providers/antigravity"
	_ "aether-cockpit/internal/providers/antigravityide"
	_ "aether-cockpit/internal/providers/claude"
	_ "aether-cockpit/internal/providers/codebuddy"
	_ "aether-cockpit/internal/providers/cursor"
	_ "aether-cockpit/internal/providers/devin"
	_ "aether-cockpit/internal/providers/geminicompat"
	_ "aether-cockpit/internal/providers/githubcopilot"
	_ "aether-cockpit/internal/providers/kiro"
	_ "aether-cockpit/internal/providers/minimax"
	_ "aether-cockpit/internal/providers/openaicompat"
	_ "aether-cockpit/internal/providers/qoder"
	_ "aether-cockpit/internal/providers/reverse"
	_ "aether-cockpit/internal/providers/trae"
	_ "aether-cockpit/internal/providers/typesafe"
	_ "aether-cockpit/internal/providers/vertex"
	_ "aether-cockpit/internal/providers/windsurf"
	_ "aether-cockpit/internal/providers/workbuddy"
	_ "aether-cockpit/internal/providers/zcode"
	_ "aether-cockpit/internal/providers/zed"
)
