// Package wsl 负责发现本机的 WSL2 发行版与它们的 $HOME，并给出
// "从 WSL 里能访问到的网关地址"所需的宿主侧信息。
//
// 为什么单独成包：会话（只读扫描）与客户端注入（写配置）都要用同一套
// 检测。两边各写一遍会出现"会话页看得见、注入页看不见"这种不一致，
// 而且每次检测都要起 wsl.exe 进程，成本不该付两遍。
//
// 两个环境变量（沿用会话侧既有语义，测试与挂载场景用）：
//   - AETHER_WSL_HOMES：路径列表（os.PathListSeparator 分隔），直接作为
//     WSL 的家目录使用，跳过发行版检测；
//   - AETHER_WSL_DISTRO：覆盖从 UNC 路径推断出的发行版名。
package wsl

import (
	"os"
	"strings"
)

// Home 是一个 WSL 家目录。
type Home struct {
	// Distro 是发行版名（如 "Ubuntu-22.04"）。
	Distro string `json:"distro"`
	// Path 是 Windows 侧可读写的家目录根，形如
	// //wsl.localhost/<distro>/home/<user>（Go 用正斜杠，底层是 UNC）。
	Path string `json:"path"`
}

// Slug 把发行版名变成安全的 id 片段：小写，非 [a-z0-9] 折成 '-'。
func Slug(distro string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(distro)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// distroFromPath 从 UNC 路径推断发行版名；推断不出时返回 fallback。
func distroFromPath(p, fallback string) string {
	if d := strings.TrimSpace(os.Getenv("AETHER_WSL_DISTRO")); d != "" {
		return d
	}
	norm := strings.ReplaceAll(p, `\`, "/")
	for _, base := range []string{"//wsl.localhost/", "//wsl$/"} {
		if strings.HasPrefix(strings.ToLower(norm), base) {
			rest := norm[len(base):]
			if i := strings.IndexByte(rest, '/'); i > 0 {
				return rest[:i]
			}
		}
	}
	return fallback
}

// Invalidate 清掉发行版检测缓存（用户装了新发行版、或改了 .wslconfig 后用）。
func Invalidate() {
	invalidateDetection()
}
