package mock

// 假上游渠道（mock-native / mock-weak / mock-responses）只服务测试与开发：
// 产品运行时默认不注册，避免真机上出现「假渠道」。
// 单元测试在 TestMain 里调用 Register()；`-dev-mock` 构建（gate smoke 用）
// 由隐藏的 dev 注册钩子调用（见 cmd/cockpit/devmock_on.go）。

import (
	"sync"

	"aether-cockpit/internal/provider"
)

var registerOnce sync.Once

// Register 注册全部 mock 渠道，可重复调用。
func Register() {
	registerOnce.Do(func() {
		provider.Register(&nativeProvider{})
		provider.Register(&weakProvider{})
		provider.Register(&nativeResponsesProvider{})
	})
}
