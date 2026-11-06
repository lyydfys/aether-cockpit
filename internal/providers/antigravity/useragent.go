package antigravity

// 官方客户端的 User-Agent 生成。
//
// 原实现把 `os_type=darwin; arch=arm64` 写死，而网关多数跑在 Windows 上——
// 这会造成"登录时是 Windows、调用时声称是 macOS/arm64"的设备身份矛盾，
// 恰恰是风控最容易抓的一类不一致。真实客户端的 UA 一定反映它自己那台机器，
// 所以这里按**本机真实运行时**生成。
//
// cl（client build id）是版本常量，跟随客户端版本走，不随机。

import (
	"fmt"
	"runtime"
	"strings"
)

const (
	antigravityHubVersion  = "2.24.0"
	antigravityClientBuild = "1000335682"
)

// antigravityUserAgent 生成形如
// `antigravity/hub/2.24.0 (aidev_client; os_type=windows; arch=x86_64; cl=1000335682)` 的 UA。
func antigravityUserAgent() string {
	return fmt.Sprintf("antigravity/hub/%s (aidev_client; os_type=%s; arch=%s; cl=%s)",
		antigravityHubVersion, antigravityOS(), antigravityArch(), antigravityClientBuild)
}

// antigravityOS 映射成客户端 UA 里的 os_type 取值。
func antigravityOS() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	default:
		return strings.ToLower(runtime.GOOS)
	}
}

// antigravityArch 映射成客户端 UA 里的 arch 取值。
func antigravityArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}
