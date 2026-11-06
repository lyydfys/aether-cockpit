package reverse

// 客户端 UA 常量。
//
// 反代渠道最容易踩的坑不是"少一个头"，而是**头里带着自己的品牌**：
// 例如 `codex_cli_rs/0.16.0 (Aether Cockpit)` 这种把真实客户端 UA 改一半的写法，
// 等于在官方指纹上盖了个"我是第三方"的章，比完全不伪装更危险。
//
// 这里统一按运行机器的真实平台生成，尽量贴近官方客户端的取值形态。

import (
	"fmt"
	"runtime"
	"strings"
)

// codexClientVersion 是伪装用的 Codex CLI 版本。
const codexClientVersion = "0.16.0"

// codexUserAgent 生成 Codex CLI 形态的 UA：`codex_cli_rs/0.16.0 (Ubuntu 22.04; x86_64)`。
//
// 平台取自**本机真实运行时**：网关就跑在用户自己的机器上，用真实平台比编一个
// 更自洽（真实客户端的 UA 也是它自己那台机器的平台）。
func codexUserAgent() string {
	return fmt.Sprintf("codex_cli_rs/%s (%s; %s)", codexClientVersion, uaOS(), uaArch())
}

// uaOS 把 runtime.GOOS 映射成官方 UA 里常见的写法。
func uaOS() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows 11"
	case "darwin":
		return "Mac OS 15.3.1"
	case "linux":
		return "Ubuntu 22.04"
	default:
		return strings.Title(runtime.GOOS)
	}
}

// uaArch 把 runtime.GOARCH 映射成官方 UA 里的架构写法。
func uaArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}
