//go:build !windows

package antigravity

import "fmt"

// readLocalCredential 在其他平台暂未实现官方客户端的凭据读取。
func readLocalCredential() ([]byte, error) {
	return nil, fmt.Errorf("当前平台不支持读取 Antigravity 本机凭据")
}

// writeLocalCredential 在其他平台只依赖 ~/.gemini 回退文件。
func writeLocalCredential([]byte) error {
	return fmt.Errorf("当前平台不支持写入 Antigravity 系统凭据")
}
