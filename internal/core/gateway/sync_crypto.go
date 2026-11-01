package gateway

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"

	"aether-cockpit/internal/core/simplecrypt"
)

// 口令密封的配置备份。
//
// magpie 的同步文件用口令密封，换一台电脑输入同一口令即可还原；
// 口令本身用本机绑定的 simplecrypt 保存，GET 时永远不回显。
// 文件格式：magic(7) | salt(16) | nonce(12) | AES-256-GCM 密文。
const (
	sealMagic    = "AETHER1"
	sealKDFIters = 120000
)

// pbkdf2SHA256 是标准 PBKDF2-HMAC-SHA256（RFC 8018）。
// 只依赖标准库：x/crypto 会给单 exe 增加依赖，而这里只需要一个 KDF。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	h := sha256.New
	hashLen := h().Size()
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(h, password)
		mac.Write(salt)
		var idx [4]byte
		binary.BigEndian.PutUint32(idx[:], uint32(block))
		mac.Write(idx[:])
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iter; i++ {
			mac.Reset()
			mac.Write(u)
			u = mac.Sum(u[:0])
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

// sealBackup 用口令加密备份；空口令原样返回（兼容旧的明文备份）。
func sealBackup(plain []byte, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return plain, nil
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	key := pbkdf2SHA256([]byte(passphrase), salt, sealKDFIters, 32)
	block, err := aes.NewCipher(key)
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
	out := make([]byte, 0, len(sealMagic)+len(salt)+len(nonce)+len(plain)+gcm.Overhead())
	out = append(out, sealMagic...)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = gcm.Seal(out, nonce, plain, nil)
	return out, nil
}

// openBackup 解密备份；明文备份（旧版/未设口令）原样返回。
func openBackup(data []byte, passphrase string) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte(sealMagic)) {
		return data, nil
	}
	if passphrase == "" {
		return nil, errors.New("备份已用口令加密：请先在同步设置里填入口令")
	}
	rest := data[len(sealMagic):]
	if len(rest) < 16+12 {
		return nil, errors.New("备份文件损坏：头部不完整")
	}
	salt, nonce, ct := rest[:16], rest[16:28], rest[28:]
	key := pbkdf2SHA256([]byte(passphrase), salt, sealKDFIters, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errors.New("口令不对：无法解开这份备份")
	}
	return plain, nil
}

func sealedBackup(data []byte) bool {
	return bytes.HasPrefix(data, []byte(sealMagic))
}

// encryptPassphrase 把口令存成本机绑定的密文（base64）。
func encryptPassphrase(pass string) (string, error) {
	enc, err := simplecrypt.Encrypt([]byte(pass))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

func decryptPassphrase(enc string) string {
	if enc == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return ""
	}
	plain, err := simplecrypt.Decrypt(raw)
	if err != nil {
		return ""
	}
	return string(plain)
}
