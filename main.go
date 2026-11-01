// Command AetherCockpit 是 Wails CLI 的根入口。
//
// Wails v2 默认在项目根目录寻找 main 包并据此生成绑定；
// 因此根目录保留一个极薄的壳，真正的桌面逻辑在 internal/desktop。
// headless 网关仍由 cmd/cockpit 提供，三者共用同一套内核。
package main

import "aether-cockpit/internal/desktop"

func main() {
	desktop.Run()
}
