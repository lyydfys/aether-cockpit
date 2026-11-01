// Package appstart 管理“登录时自动启动 Aether Cockpit”（Windows 用户级 Run 项）。
//
// 只对桌面端有意义：无头 CLI 的启动参数与数据目录由用户脚本决定，
// 写一个裸 exe 路径会让它用默认参数在错误目录启动，因此网关只在
// DesktopApp 模式下暴露该开关。
package appstart

import (
	"os"
	"strings"
)

// RunValueName 是 HKCU Run 下的值名。
const RunValueName = "AetherCockpit"

// Status 是开机自启的对外状态。
type Status struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Command   string `json:"command,omitempty"`
}

// CurrentCommand 返回当前可执行文件的启动命令（带引号，防止空格路径）。
func CurrentCommand() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return `"` + exe + `"`, nil
}

// sameCommand 比较两条启动命令（忽略大小写与引号差异）。
func sameCommand(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.Trim(s, `"`)
		return strings.ToLower(s)
	}
	return norm(a) != "" && norm(a) == norm(b)
}
