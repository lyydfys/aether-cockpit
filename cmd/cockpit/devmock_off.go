//go:build !dev_mock

package main

// registerDevMock 在正式构建里是空操作：产品运行时不存在 mock 渠道。
func registerDevMock() {}
