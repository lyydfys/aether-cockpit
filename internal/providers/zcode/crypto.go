package zcode

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strings"
)

// ZCode 凭据文件里的密钥值是加密的（官方开源实现见
// apps/zcode-cli/packages/adapters/src/auth/credential-cipher.ts）：
//
//	enc:v1:<iv_b64url>.<authTag_b64url>.<ciphertext_b64url>
//
// AES-256-GCM，key = SHA256(secret)，secret 优先取环境变量
// ZCODE_CREDENTIAL_SECRET，否则由平台/家目录/用户名派生（换机器即失效，
// 与官方一致）。这里按同一算法解密/加密，不引入额外依赖。
const encryptedValuePrefix = "enc:v1:"

// nodePlatform 复刻 Node os.platform() 的取值。
func nodePlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	default:
		return runtime.GOOS
	}
}

// currentUserName 复刻 Node os.userInfo().username（去域前缀）。
func currentUserName() string {
	if v := strings.TrimSpace(os.Getenv("USERNAME")); v != "" {
		return v
	}
	if u, err := user.Current(); err == nil {
		name := u.Username
		if i := strings.LastIndexAny(name, `\/`); i >= 0 {
			name = name[i+1:]
		}
		if name != "" {
			return name
		}
	}
	return "unknown"
}

// credentialSecret 复刻官方 resolveCredentialSecret。
func credentialSecret() string {
	if v := strings.TrimSpace(os.Getenv("ZCODE_CREDENTIAL_SECRET")); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return "zcode-credential-fallback:" + nodePlatform() + ":" + home + ":" + currentUserName()
}

func credentialKey() []byte {
	sum := sha256.Sum256([]byte(credentialSecret()))
	return sum[:]
}

// isEncryptedCredential 判断值是否为官方加密信封。
func isEncryptedCredential(value string) bool {
	return strings.HasPrefix(value, encryptedValuePrefix)
}

// decryptCredential 解密 enc:v1: 信封；未加密的值原样返回。
func decryptCredential(value string) (string, error) {
	if !isEncryptedCredential(value) {
		return value, nil
	}
	parts := strings.Split(strings.TrimPrefix(value, encryptedValuePrefix), ".")
	if len(parts) != 3 {
		return "", errors.New("凭据密文格式非法")
	}
	iv, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(iv) != 12 {
		return "", errors.New("凭据 IV 非法")
	}
	tag, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(tag) != 16 {
		return "", errors.New("凭据 authTag 非法")
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errors.New("凭据密文非法")
	}
	block, err := aes.NewCipher(credentialKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plain, err := gcm.Open(nil, iv, append(ciphertext, tag...), nil)
	if err != nil {
		return "", errors.New("凭据解密失败（密钥不匹配或密文损坏）")
	}
	return string(plain), nil
}

// encryptCredential 生成官方格式的 enc:v1: 信封。
func encryptCredential(plain string) (string, error) {
	block, err := aes.NewCipher(credentialKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, iv, []byte(plain), nil)
	body := sealed[:len(sealed)-gcm.Overhead()]
	tag := sealed[len(sealed)-gcm.Overhead():]
	return fmt.Sprintf("%s%s.%s.%s", encryptedValuePrefix,
		base64.RawURLEncoding.EncodeToString(iv),
		base64.RawURLEncoding.EncodeToString(tag),
		base64.RawURLEncoding.EncodeToString(body)), nil
}
