//go:build !windows

package sessions

import (
	"os"
	"strings"
)

// 非 Windows 没有 WSL 的 UNC 视图；仍允许用 AETHER_WSL_HOMES 注入
// （测试或挂载了外部目录的场景）。
func wslRoots() []sessionRoot {
	v := strings.TrimSpace(os.Getenv("AETHER_WSL_HOMES"))
	if v == "" {
		return nil
	}
	distro := strings.TrimSpace(os.Getenv("AETHER_WSL_DISTRO"))
	if distro == "" {
		distro = "WSL"
	}
	out := []sessionRoot{}
	for _, p := range strings.Split(v, string(os.PathListSeparator)) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, sessionRoot{Home: p, WSL: distro})
		}
	}
	return out
}
