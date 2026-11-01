//go:build dev_mock

package main

import "aether-cockpit/internal/providers/mock"

// registerDevMock 在 -tags dev_mock 构建里注册假上游，供 gate smoke 使用；
// 正式产物不带该标签，因此不含任何 mock 渠道注册。
func registerDevMock() { mock.Register() }
