//go:build !windows

package transport

import (
	"net/http"
	"net/url"
)

// sysProxyFunc 在非 Windows 上直接退回环境变量（macOS/Linux 的"系统代理"
// 本来就通过环境变量或桌面设置暴露，不需要单独读注册表）。
func sysProxyFunc() func(*http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment
}
