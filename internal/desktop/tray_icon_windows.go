//go:build windows

package desktop

import (
	_ "embed"

	systray "github.com/energye/systray"
)

//go:embed tray.ico
var trayIconICO []byte

func setTrayIcon() {
	// Windows 托盘要求 ICO 字节，PNG 不会被识别。
	systray.SetIcon(trayIconICO)
}
