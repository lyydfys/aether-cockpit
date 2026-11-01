//go:build windows

package antigravity

import (
	"fmt"
	"runtime"
	"unsafe"

	"syscall"
)

// readLocalCredential 读取 Windows 凭据管理器里的 Antigravity 登录态。
//
// CredReadW 返回的 CREDENTIAL 结构体里 CredentialBlob 是原始 UTF-8 JSON，
// 不需要再做 base64 或 DPAPI 解密。
func readLocalCredential() ([]byte, error) {
	blob, _, err := readLocalCredentialMeta()
	return blob, err
}

// readLocalCredentialMeta 同时返回条目现有 UserName（写回时沿用）。
func readLocalCredentialMeta() ([]byte, string, error) {
	target, err := syscall.UTF16PtrFromString("gemini:antigravity")
	if err != nil {
		return nil, "", err
	}

	var cred *winCredential
	r, _, lastErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)),
		1, // CRED_TYPE_GENERIC
		0,
		uintptr(unsafe.Pointer(&cred)),
	)
	if r == 0 {
		return nil, "", fmt.Errorf("CredRead(gemini:antigravity) 失败: %v", lastErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))

	if cred == nil || cred.CredentialBlobSize == 0 || cred.CredentialBlob == nil {
		return nil, "", fmt.Errorf("Antigravity 凭据为空")
	}
	blob := unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)
	out := make([]byte, len(blob))
	copy(out, blob)
	user := ""
	if cred.UserName != nil {
		user = syscall.UTF16ToString(unsafe.Slice(cred.UserName, 1<<15))
	}
	return out, user, nil
}

// writeLocalCredential 把登录态写回 Windows 凭据管理器 target=gemini:antigravity。
//
// 官方客户端（以及 cockpit-tools 的切号实现）都从这一条目读取 token；
// UserName 优先沿用现有条目，避免某些版本按用户名匹配。
func writeLocalCredential(blob []byte) error {
	if len(blob) == 0 {
		return fmt.Errorf("Antigravity 凭据为空")
	}
	user := "antigravity"
	if _, existing, err := readLocalCredentialMeta(); err == nil && existing != "" {
		user = existing
	}

	target, err := syscall.UTF16PtrFromString("gemini:antigravity")
	if err != nil {
		return err
	}
	comment, err := syscall.UTF16PtrFromString("Aether Cockpit injected token")
	if err != nil {
		return err
	}
	userPtr, err := syscall.UTF16PtrFromString(user)
	if err != nil {
		return err
	}

	cred := winCredential{
		Type:               1, // CRED_TYPE_GENERIC
		TargetName:         target,
		Comment:            comment,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            2, // CRED_PERSIST_LOCAL_MACHINE
		UserName:           userPtr,
	}
	r, _, lastErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	runtime.KeepAlive(blob)
	runtime.KeepAlive(target)
	runtime.KeepAlive(comment)
	runtime.KeepAlive(userPtr)
	if r == 0 {
		return fmt.Errorf("CredWrite(gemini:antigravity) 失败: %v", lastErr)
	}
	return nil
}

type winCredential struct {
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
	advapi32       = syscall.NewLazyDLL("advapi32.dll")
	procCredReadW  = advapi32.NewProc("CredReadW")
	procCredWriteW = advapi32.NewProc("CredWriteW")
	procCredFree   = advapi32.NewProc("CredFree")
)
