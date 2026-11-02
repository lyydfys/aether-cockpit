//go:build darwin

package desktop

import (
	_ "embed"

	systray "github.com/energye/systray"
)

// template 用黑色剪影 + alpha，系统会按菜单栏明暗自动着色；
// regular 给不支持模板图的环境兜底。
//
//go:embed trayTemplate.png
var trayTemplatePNG []byte

//go:embed trayRegular.png
var trayRegularPNG []byte

func setTrayIcon() {
	systray.SetTemplateIcon(trayTemplatePNG, trayRegularPNG)
}
