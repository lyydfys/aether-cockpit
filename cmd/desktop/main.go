// Command desktop 是 Aether Cockpit 的桌面端入口。
//
// 实际实现位于 internal/desktop：这样 Wails CLI 在项目根目录构建时，
// 根目录的 main.go 与这里的 main.go 可以共用同一套桌面逻辑。
package main

import "aether-cockpit/internal/desktop"

func main() {
	desktop.Run()
}
