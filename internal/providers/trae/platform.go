package trae

// 平台相关字段的取值。
//
// 之前 deviceType / osVersion / OSInfo 全部写死成 Windows：网关跑在 macOS 或
// Linux 上时，请求仍声称自己是 Windows 10 —— 与登录时真实的系统信息冲突，
// 是最容易被关联的一类不一致。这里按运行平台生成，Windows 上保持抓包原值。

import "runtime"

// traeDeviceType 返回 x-device-type / OSInfo 的取值。
func traeDeviceType() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "darwin"
	case "linux":
		return "linux"
	default:
		return runtime.GOOS
	}
}

// traeOSVersion 返回 x-os-version / OSVersion 的取值。
//
// Windows 保持抓包里的 "Windows 10"（官方客户端在这个版本上就是如此上报）。
func traeOSVersion() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows 10"
	case "darwin":
		return "macOS 14"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}
