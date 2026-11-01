package transport

import (
	"net"
	"runtime"
	"strings"
)

// tunInterfaceHints 是各常见代理客户端 TUN 虚拟网卡的名称特征。
// 命中任意一个，说明系统很可能处于全局劫持状态，此时 direct_force 会失效。
var tunInterfaceHints = []string{
	"tun", "wintun", "tap", "clash", "mihomo", "singbox", "sing-box",
	"wireguard", "wg0", "tailscale", "zerotier", "utun",
}

// TUNStatus 记录 TUN 检测结果。
type TUNStatus struct {
	// Detected 是否疑似存在 TUN 虚拟网卡。
	Detected bool `json:"detected"`
	// Interfaces 命中的网卡名。
	Interfaces []string `json:"interfaces,omitempty"`
	// Warning 给 UI 直接展示的提示语。
	Warning string `json:"warning,omitempty"`
}

// DetectTUN 检测系统是否开启了 TUN 模式的全局代理。
//
// 为什么必须检测：direct_force 的实现只是"不使用代理配置"，
// 但 TUN 模式是在网络栈层面接管所有 TCP 流量，Go 进程即使不读代理配置，
// 流量依然会进入虚拟网卡被转发到海外节点。
//
// 对 CN 渠道而言这意味着：用户以为在直连，实际是海外 IP 在登录国内账号 ——
// 这正是触发厂商"异地登录"风控的典型场景，必须显式告知用户。
func DetectTUN() TUNStatus {
	ifaces, err := net.Interfaces()
	if err != nil {
		return TUNStatus{}
	}

	var hits []string
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 {
			continue
		}
		if ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(ifi.Name)
		for _, hint := range tunInterfaceHints {
			if strings.Contains(name, hint) {
				hits = append(hits, ifi.Name)
				break
			}
		}
	}

	if len(hits) == 0 {
		return TUNStatus{}
	}

	return TUNStatus{
		Detected:   true,
		Interfaces: hits,
		Warning: "检测到 TUN 虚拟网卡（" + strings.Join(hits, ", ") + "）。" +
			"国内渠道的【强制直连】在此模式下无法真正生效，流量仍会被转发。" +
			"请为该进程或目标域名配置直连规则，或在代理客户端中切换为规则/系统代理模式。",
	}
}

// Platform 返回规范化的平台标识，用于填充设备指纹。
func Platform() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "darwin":
		return "darwin"
	default:
		return runtime.GOOS
	}
}
