// Package simplecrypt 提供一层"绑定本机"的对称加密。
//
// 目的不是防御有决心的攻击者（密钥就在本机，做不到），而是防止：
//   - 账号文件被误同步到网盘/Git 仓库后直接可用；
//   - 换个机器拷走文件就能复刻全部凭证。
//
// 密钥由本机主机名 + 用户配置目录派生，换机器即解不开，符合自用场景的预期。
package simplecrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"os"
)

// deriveKey 派生本机绑定的 256 位密钥。
func deriveKey() []byte {
	host, _ := os.Hostname()
	cfgDir, _ := os.UserConfigDir()
	home, _ := os.UserHomeDir()
	seed := host + "|" + cfgDir + "|" + home + "|aether-cockpit::v1"
	sum := sha256.Sum256([]byte(seed))
	return sum[:]
}

// Encrypt 加密明文，输出 nonce||ciphertext。
func Encrypt(plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(deriveKey())
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

// Decrypt 解密 Encrypt 的输出。
func Decrypt(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(deriveKey())
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("simplecrypt: 密文长度不足")
	}
	nonce, ct := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}
