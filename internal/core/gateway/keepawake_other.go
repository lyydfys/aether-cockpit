//go:build !windows

package gateway

import "fmt"

// 非 Windows 平台不影响系统电源策略；开启时明确报不支持，避免假装生效。
func setKeepAwake(on bool) error {
	if on {
		return fmt.Errorf("保持唤醒目前只在 Windows 上生效")
	}
	return nil
}
