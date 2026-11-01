//go:build windows

package qoder

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"syscall"
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	crypt32            = syscall.NewLazyDLL("crypt32.dll")
	procCryptUnprotect = crypt32.NewProc("CryptUnprotectData")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree      = kernel32.NewProc("LocalFree")
)

func dpapiUnprotect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("DPAPI 输入为空")
	}
	in := dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var out dataBlob
	r, _, lastErr := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData 失败: %v", lastErr)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	buf := unsafe.Slice(out.pbData, out.cbData)
	res := make([]byte, len(buf))
	copy(res, buf)
	return res, nil
}

func readDesktopAuth(prof profile) (*desktopAuth, string, error) {
	dir := filepath.Join(os.Getenv("APPDATA"), prof.appDir)
	authPath := filepath.Join(dir, "auth.v1.dat")
	raw, err := os.ReadFile(authPath)
	if err != nil {
		return nil, "", err
	}

	key, err := chromiumKey(filepath.Join(dir, "Local State"))
	if err != nil {
		return nil, "", err
	}
	plain, err := decryptV10(raw, key)
	if err != nil {
		return nil, "", err
	}
	auth, err := parseDesktopAuthJSON(plain)
	if err != nil {
		return nil, "", err
	}
	return auth, "local_import:" + authPath, nil
}

// chromiumKey 读取 Electron safeStorage 使用的 AES-256 密钥。
func chromiumKey(localStatePath string) ([]byte, error) {
	raw, err := os.ReadFile(localStatePath)
	if err != nil {
		return nil, err
	}
	var state struct {
		OSCrypt struct {
			EncryptedKey string `json:"encrypted_key"`
		} `json:"os_crypt"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	blob, err := base64.StdEncoding.DecodeString(state.OSCrypt.EncryptedKey)
	if err != nil {
		return nil, err
	}
	if len(blob) < 8 || string(blob[:5]) != "DPAPI" {
		return nil, fmt.Errorf("Local State encrypted_key 不是 DPAPI blob")
	}
	return dpapiUnprotect(blob[5:])
}

// decryptV10 解密 Electron safeStorage 的 "v10" AES-256-GCM 文件。
func decryptV10(raw, key []byte) ([]byte, error) {
	if len(raw) < 3+12+16 || string(raw[:3]) != "v10" {
		return nil, fmt.Errorf("auth.v1.dat 不是 v10 格式")
	}
	nonce := raw[3:15]
	payload := raw[15:]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, payload, nil)
}
