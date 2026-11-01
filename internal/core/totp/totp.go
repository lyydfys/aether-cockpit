// Package totp 实现 RFC 6238 TOTP，用于本机 2FA 验证码管理。
//
// 只依赖标准库；不保存明文密钥（存储层用 simplecrypt 加密）。
package totp

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"math"
	"strings"
	"time"
)

// DefaultDigits / DefaultPeriod 是绝大多数平台的默认值。
const (
	DefaultDigits = 6
	DefaultPeriod = 30
)

// Normalize 把用户粘贴的密钥规整成可解码的 base32（大写、去空格与填充）。
func Normalize(secret string) string {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	return strings.TrimRight(s, "=")
}

// Validate 校验密钥能否解码；不能解码时返回错误。
func Validate(secret string) error {
	_, err := decodeSecret(secret)
	return err
}

func decodeSecret(secret string) ([]byte, error) {
	s := Normalize(secret)
	if s == "" {
		return nil, errors.New("totp: 密钥为空")
	}
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("totp: 密钥不是合法 base32: %w", err)
	}
	if len(raw) == 0 {
		return nil, errors.New("totp: 密钥为空")
	}
	return raw, nil
}

func hashFor(algorithm string) (func() hash.Hash, error) {
	switch strings.ToUpper(strings.TrimSpace(algorithm)) {
	case "", "SHA1":
		return sha1.New, nil
	case "SHA256":
		return sha256.New, nil
	case "SHA512":
		return sha512.New, nil
	default:
		return nil, fmt.Errorf("totp: 不支持的算法 %q（SHA1/SHA256/SHA512）", algorithm)
	}
}

// Digits 把非法的位数收敛到 4..10，缺省 6。
func Digits(n int) int {
	if n == 0 {
		return DefaultDigits
	}
	if n < 4 {
		return 4
	}
	if n > 10 {
		return 10
	}
	return n
}

// Period 把非法的周期收敛到 5..300 秒，缺省 30。
func Period(n int) int {
	if n == 0 {
		return DefaultPeriod
	}
	if n < 5 {
		return 5
	}
	if n > 300 {
		return 300
	}
	return n
}

// Code 计算 at 时刻的验证码，并返回距下一次变化的秒数。
func Code(secret string, at time.Time, digits, period int, algorithm string) (string, int, error) {
	raw, err := decodeSecret(secret)
	if err != nil {
		return "", 0, err
	}
	mac, err := hashFor(algorithm)
	if err != nil {
		return "", 0, err
	}
	digits = Digits(digits)
	period = Period(period)

	counter := uint64(at.Unix() / int64(period))
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)

	h := hmac.New(mac, raw)
	h.Write(msg[:])
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[off])&0x7f)<<24 |
		uint32(sum[off+1])<<16 |
		uint32(sum[off+2])<<8 |
		uint32(sum[off+3])
	mod := uint32(math.Pow10(digits))
	code := fmt.Sprintf("%0*d", digits, value%mod)
	remaining := period - int(at.Unix()%int64(period))
	if remaining <= 0 {
		remaining = period
	}
	return code, remaining, nil
}
