//go:build !windows

package qoder

import "fmt"

func readDesktopAuth(prof profile) (*desktopAuth, string, error) {
	return nil, "", fmt.Errorf("当前平台暂不支持读取 Qoder 桌面登录态")
}
