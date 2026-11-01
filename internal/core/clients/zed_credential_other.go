//go:build !windows

package clients

import "fmt"

func writeZedCredential(url, apiKey string) error {
	return fmt.Errorf("当前平台暂不支持写入 Zed 系统凭据")
}

func deleteZedCredential(url string) error { return nil }
