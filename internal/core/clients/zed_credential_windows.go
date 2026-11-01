//go:build windows

package clients

import (
	"fmt"
	"syscall"
	"unsafe"
)

type zedWinCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	zedAdvapi32       = syscall.NewLazyDLL("advapi32.dll")
	zedProcCredWriteW = zedAdvapi32.NewProc("CredWriteW")
	zedProcCredDelete = zedAdvapi32.NewProc("CredDeleteW")
	zedProcCredReadW  = zedAdvapi32.NewProc("CredReadW")
	zedProcCredFree   = zedAdvapi32.NewProc("CredFree")
)

func zedTarget(url string) string { return "zed:url=" + url }

// writeZedCredential 写 Zed 认的 Windows 凭据（generic、LOCAL_MACHINE、Bearer）。
func writeZedCredential(url, apiKey string) error {
	if apiKey == "" {
		return fmt.Errorf("API Key 为空")
	}
	blob := []byte(apiKey)
	target, err := syscall.UTF16PtrFromString(zedTarget(url))
	if err != nil {
		return err
	}
	user, err := syscall.UTF16PtrFromString("Bearer")
	if err != nil {
		return err
	}
	cred := zedWinCredential{
		Type:               1, // CRED_TYPE_GENERIC
		TargetName:         target,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            2, // CRED_PERSIST_LOCAL_MACHINE
		UserName:           user,
	}
	r, _, lastErr := zedProcCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	if r == 0 {
		return fmt.Errorf("CredWrite(%s) 失败: %v", zedTarget(url), lastErr)
	}
	return nil
}

// deleteZedCredential 删除该 URL 的凭据；不存在不算错误。
func deleteZedCredential(url string) error {
	target, err := syscall.UTF16PtrFromString(zedTarget(url))
	if err != nil {
		return err
	}
	r, _, lastErr := zedProcCredDelete.Call(uintptr(unsafe.Pointer(target)), 1, 0)
	if r == 0 {
		if errno, ok := lastErr.(syscall.Errno); ok && errno == 1168 { // ERROR_NOT_FOUND
			return nil
		}
		return fmt.Errorf("CredDelete(%s) 失败: %v", zedTarget(url), lastErr)
	}
	return nil
}

// readZedCredential 读回（测试与诊断用）。
func readZedCredential(url string) (string, error) {
	target, err := syscall.UTF16PtrFromString(zedTarget(url))
	if err != nil {
		return "", err
	}
	var cred *zedWinCredential
	r, _, lastErr := zedProcCredReadW.Call(uintptr(unsafe.Pointer(target)), 1, 0, uintptr(unsafe.Pointer(&cred)))
	if r == 0 {
		return "", fmt.Errorf("CredRead(%s) 失败: %v", zedTarget(url), lastErr)
	}
	defer zedProcCredFree.Call(uintptr(unsafe.Pointer(cred)))
	if cred == nil || cred.CredentialBlobSize == 0 || cred.CredentialBlob == nil {
		return "", fmt.Errorf("凭据为空")
	}
	return string(unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)), nil
}
