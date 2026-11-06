package cursor

// Cursor 桌面端请求使用的浏览器形态 UA。
//
// cursor.com 的用量接口由 Electron 渲染进程调用，UA 是标准 Chromium 形态。
// 写死 `Windows NT 10.0` 会让 macOS/Linux 上的网关"自称 Windows"——与本地
// 登录态来源（真实系统）冲突，属于很容易被关联的不一致。这里按运行平台生成。

import "runtime"

// cursorUserAgent 返回与本机平台一致的浏览器 UA。
func cursorUserAgent() string {
	return "Mozilla/5.0 (" + cursorPlatformToken() + ") AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
}

// cursorPlatformToken 生成 UA 括号内的平台标识。
func cursorPlatformToken() string {
	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	switch runtime.GOOS {
	case "windows":
		return "Windows NT 10.0; Win64; " + arch
	case "darwin":
		if arch == "arm64" {
			return "Macintosh; Apple Silicon Mac OS X 10_15_7"
		}
		return "Macintosh; Intel Mac OS X 10_15_7"
	default:
		return "X11; Linux " + arch
	}
}
