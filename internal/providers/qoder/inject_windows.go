//go:build windows

package qoder

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

// 一键切号（Windows）：把账号写回 Qoder 桌面客户端的 auth.v1.dat。
//
// 文件是 Electron safeStorage 的 v10 格式（AES-256-GCM，密钥由 Local State
// 的 DPAPI blob 解出）。只更新 token/refreshToken/expiresAt 与 user.*，
// 其余字段原样保留；写前备份为 auth.v1.dat.aether.bak。

var procCryptProtect = crypt32.NewProc("CryptProtectData")

func dpapiProtect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("DPAPI 输入为空")
	}
	in := dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var out dataBlob
	r, _, lastErr := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0,
		0x1, // CRYPTPROTECT_UI_FORBIDDEN
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData 失败: %v", lastErr)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	buf := unsafe.Slice(out.pbData, out.cbData)
	res := make([]byte, len(buf))
	copy(res, buf)
	return res, nil
}

// encryptV10 生成 Electron safeStorage 的 "v10" 文件内容。
func encryptV10(plain, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	sealed := gcm.Seal(nil, nonce, plain, nil)
	out := make([]byte, 0, 3+len(nonce)+len(sealed))
	out = append(out, 'v', '1', '0')
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// readDesktopAuthRaw 读 auth.v1.dat 的明文与解密密钥。
func readDesktopAuthRaw(prof profile) (string, []byte, []byte, error) {
	dir := filepath.Join(os.Getenv("APPDATA"), prof.appDir)
	authPath := filepath.Join(dir, "auth.v1.dat")
	raw, err := os.ReadFile(authPath)
	if err != nil {
		return "", nil, nil, err
	}
	key, err := chromiumKey(filepath.Join(dir, "Local State"))
	if err != nil {
		return "", nil, nil, err
	}
	plain, err := decryptV10(raw, key)
	if err != nil {
		return "", nil, nil, err
	}
	return authPath, plain, key, nil
}

// InjectLocalAccount 实现 provider.LocalInjectCapability。
func (p *Provider) InjectLocalAccount(ctx context.Context, env *provider.AccountEnv) (*provider.InjectResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	token := strings.TrimSpace(env.GetCredential("token"))
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"账号缺少 token：先刷新或重新登录，再写入本机 Qoder").WithProvider(p.profile.id)
	}
	authPath, plain, key, err := readDesktopAuthRaw(p.profile)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeNoAccount,
			"没有找到 Qoder 桌面登录态（"+p.profile.appDir+"）；请先登录客户端，或手动粘贴 token").
			WithProvider(p.profile.id)
	}
	var doc map[string]any
	if err := json.Unmarshal(plain, &doc); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "Qoder 登录态无法解析："+err.Error()).
			WithProvider(p.profile.id)
	}
	doc["token"] = token
	if v := strings.TrimSpace(env.GetCredential("refresh_token")); v != "" {
		doc["refreshToken"] = v
	}
	if v := strings.TrimSpace(env.GetCredential("expires_at")); v != "" {
		doc["expiresAt"] = v
	}
	user, _ := doc["user"].(map[string]any)
	if user == nil {
		user = map[string]any{}
	}
	if v := strings.TrimSpace(env.GetCredential("user_id")); v != "" {
		user["id"] = v
	}
	if v := strings.TrimSpace(env.GetCredential("user_name")); v != "" {
		user["name"] = v
	}
	if v := strings.TrimSpace(env.GetCredential("user_email")); v != "" {
		user["email"] = v
	}
	doc["user"] = user

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	sealed, err := encryptV10(out, key)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "加密 Qoder 登录态失败："+err.Error()).
			WithProvider(p.profile.id)
	}

	backup := authPath + ".aether.bak"
	if err := copyFileQoder(authPath, backup); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "备份 auth.v1.dat 失败，已中止写入："+err.Error()).
			WithProvider(p.profile.id)
	}
	if err := writeFileAtomicQoder(authPath, sealed); err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "写入 auth.v1.dat 失败："+err.Error()).
			WithProvider(p.profile.id)
	}
	return &provider.InjectResult{
		Target:      "file:" + authPath,
		Files:       []string{backup},
		RestartHint: "完全退出并重启 Qoder 后生效（运行中的客户端会用内存里的旧登录态覆盖）。",
	}, nil
}

func copyFileQoder(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func writeFileAtomicQoder(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".qoder-auth-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

