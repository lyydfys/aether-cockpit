package desktop

import (
	_ "embed"
	"fmt"

	systray "github.com/energye/systray"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// runTray 在独立 goroutine 里启动托盘。
//
// 为什么不用 Wails 内置能力：Wails v2 没有系统托盘（v3 才内置），
// 所以用 energye/systray 补。它内部维护自己的消息循环，与 Wails 的主线程互不干扰。
func (a *App) runTray() {
	defer func() {
		if r := recover(); r != nil {
			// 托盘起不来不应该影响主窗口使用。
			fmt.Printf("[WARN] 托盘启动失败: %v\n", r)
		}
	}()
	systray.Run(a.onTrayReady, func() {})
}

func (a *App) onTrayReady() {
	systray.SetIcon(makeTrayIcon())
	systray.SetTitle("Aether Cockpit")
	systray.SetTooltip("Aether Cockpit · 本地算力网关 " + a.addr)

	// energye/systray 用回调式 API：每个菜单项注册自己的 Click 处理。
	mOpen := systray.AddMenuItem("打开主界面", "显示窗口")
	mOpen.Click(func() {
		wailsruntime.WindowShow(a.ctx)
	})

	mCopy := systray.AddMenuItem("复制网关地址", "http://"+a.addr)
	mCopy.Click(func() {
		addr := "http://" + a.addr
		wailsruntime.ClipboardSetText(a.ctx, addr)
		wailsruntime.MessageDialog(a.ctx, wailsruntime.MessageDialogOptions{
			Type:    wailsruntime.InfoDialog,
			Title:   "已复制",
			Message: "网关地址已复制到剪贴板：\n" + addr,
		})
	})

	mCheckin := systray.AddMenuItem("一键全部签到", "按串行抖动队列执行")
	mCheckin.Click(func() {
		n := a.sched.RunAll("tray")
		msg := fmt.Sprintf("已把 %d 个账号的任务加入队列。\n执行会在后台串行进行，账号之间插入 2~6 秒随机间隔。", n)
		if n == 0 {
			msg = "当前没有可执行的签到任务（可能今日已完成，或没有可用账号）。"
		}
		wailsruntime.MessageDialog(a.ctx, wailsruntime.MessageDialogOptions{
			Type:    wailsruntime.InfoDialog,
			Title:   "签到已提交",
			Message: msg,
		})
	})

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("退出", "关闭网关并退出")
	mQuit.Click(func() {
		wailsruntime.Quit(a.ctx)
	})

	// 托盘图标双击/单击唤起窗口，符合 Windows 用户习惯。
	systray.SetOnDClick(func(menu systray.IMenu) {
		wailsruntime.WindowShow(a.ctx)
	})
	systray.SetOnClick(func(menu systray.IMenu) {
		_ = menu.ShowMenu()
	})
}

// stopTray 关闭托盘。
func (a *App) stopTray() {
	defer func() { _ = recover() }()
	systray.Quit()
}

// buildAppMenu 构造应用菜单（macOS 菜单栏 / Windows 窗口菜单）。
func buildAppMenu(a *App) *menu.Menu {
	m := menu.NewMenu()

	file := m.AddSubmenu("文件")
	file.Append(menu.Text("复制网关地址", keys.CmdOrCtrl("c"), func(_ *menu.CallbackData) {
		wailsruntime.ClipboardSetText(a.ctx, "http://"+a.addr)
	}))
	file.Append(menu.Separator())
	file.Append(menu.Text("退出", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		wailsruntime.Quit(a.ctx)
	}))

	actions := m.AddSubmenu("操作")
	actions.Append(menu.Text("一键全部签到", nil, func(_ *menu.CallbackData) {
		a.sched.RunAll("menu")
	}))
	actions.Append(menu.Text("打开日志目录", nil, func(_ *menu.CallbackData) {
		wailsruntime.BrowserOpenURL(a.ctx, "http://"+a.addr)
	}))

	return m
}

//go:embed tray.ico
var trayIconICO []byte

// makeTrayIcon 返回 MAC 品牌 ICO（与 build/windows/icon.ico 同源）。
// Windows 托盘要求 ICO 字节，PNG 不会被识别。
func makeTrayIcon() []byte { return trayIconICO }
