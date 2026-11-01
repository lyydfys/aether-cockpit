package trae

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Trae 客户端 storage.json 里的 tc 加密格式（与 trae-decrypt.js 逐字节一致）：
//
//	base64( 6 字节头 | 32 字节随机数 | AES-128-CBC 密文 )
//
// 密钥/IV 由 SHA512(随机数) 与固定盐对异或后再 SHA512 派生；
// 明文 = SHA512(payload) 64 字节摘要 + payload。
const (
	tcHeaderSize  = 6
	tcRandomLen   = 32
	tcHashSize    = 64
	tcAESKeySize  = 16
	tcIVSize      = 16
	tcPayloadPad  = 16
	tcEncTypeAES  = "AES"
	tcEncTypeAESP = "AES_PRIVATE"
)

var (
	saltA = []byte{82, 9, 106, 213, 48, 54, 165, 56, 191, 64, 163, 158, 129, 243, 215, 251, 124, 227, 57, 130, 155, 47, 255, 135, 52, 142, 67, 68, 196, 222, 233, 203, 84, 123, 148, 50, 166, 194, 35, 61, 238, 76, 149, 11, 66, 250, 195, 78, 8, 46, 161, 102, 40, 217, 36, 178, 118, 91, 162, 73, 109, 139, 209, 37}
	saltB = []byte{31, 221, 168, 51, 136, 7, 199, 49, 177, 18, 16, 89, 39, 128, 236, 95, 96, 81, 127, 169, 25, 181, 74, 13, 45, 229, 122, 159, 147, 201, 156, 239, 160, 224, 59, 77, 174, 42, 245, 176, 200, 235, 187, 60, 131, 83, 153, 97, 23, 43, 4, 126, 186, 119, 214, 38, 225, 105, 20, 99, 85, 33, 12, 125}
	saltC = []byte{191, 192, 216, 250, 122, 246, 220, 97, 31, 254, 98, 27, 8, 72, 71, 176, 135, 99, 96, 18, 127, 101, 203, 104, 211, 102, 191, 125, 37, 72, 150, 156, 51, 229, 121, 35, 17, 153, 141, 177, 110, 131, 150, 128, 172, 255, 254, 6, 18, 140, 55, 62, 236, 249, 135, 64, 135, 12, 117, 4, 89, 149, 168, 209}
	saltD = []byte{246, 204, 26, 232, 232, 70, 129, 109, 223, 146, 169, 242, 23, 241, 105, 145, 50, 196, 165, 42, 254, 120, 3, 54, 244, 207, 209, 85, 53, 6, 138, 106, 175, 148, 31, 204, 186, 186, 165, 182, 87, 142, 49, 10, 39, 110, 26, 154, 86, 56, 173, 125, 18, 64, 198, 225, 99, 99, 83, 82, 191, 134, 76, 170}
)

// isTcEncrypted 判断字节流是否带 tc 头。
func isTcEncrypted(buf []byte) bool {
	return len(buf) >= tcHeaderSize && buf[0] == 0x74 && buf[1] == 0x63
}

// detectTcEncType 识别头部的两种加密类型。
func detectTcEncType(header []byte) string {
	if len(header) < tcHeaderSize {
		return "UNKNOWN"
	}
	if header[0] == 0x74 && header[1] == 0x63 && header[2] == 0x05 &&
		header[3] == 0x10 && header[4] == 0x00 && header[5] == 0x00 {
		return tcEncTypeAES
	}
	if header[0] == 18 && header[1] == 57 && header[2] == 32 &&
		header[3] == 32 && header[4] == 2 && header[5] == 3 {
		return tcEncTypeAESP
	}
	return "UNKNOWN"
}

func xorSalts(a, b []byte, n int) []byte {
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = a[i] ^ b[i]
	}
	return out
}

// deriveTcKeyAndIV 从 32 字节随机数派生 AES-128 的 key 与 IV。
func deriveTcKeyAndIV(randomBytes []byte, encType string) (key, iv []byte) {
	var salt []byte
	if encType == tcEncTypeAESP {
		salt = xorSalts(saltC, saltD, tcHashSize)
	} else {
		salt = xorSalts(saltA, saltB, tcHashSize)
	}
	hashOfRandom := sha512.Sum512(randomBytes)
	combined := make([]byte, 0, tcHashSize+tcHashSize)
	combined = append(combined, hashOfRandom[:]...)
	combined = append(combined, salt...)
	final := sha512.Sum512(combined)
	return final[:tcAESKeySize], final[tcAESKeySize : tcAESKeySize+tcIVSize]
}

// decryptTcBuffer 解密 tc 密文，返回其中的 UTF-8 明文（JSON 字符串）。
func decryptTcBuffer(buf []byte) (string, error) {
	if len(buf) < tcHeaderSize+tcRandomLen+tcHashSize+tcPayloadPad {
		return "", fmt.Errorf("tc 密文过短: %d", len(buf))
	}
	header := buf[:tcHeaderSize]
	encType := detectTcEncType(header)
	if encType == "UNKNOWN" {
		return "", fmt.Errorf("未知的 tc 加密类型: %x", header)
	}
	randomBytes := buf[tcHeaderSize : tcHeaderSize+tcRandomLen]
	ciphertext := buf[tcHeaderSize+tcRandomLen:]
	if len(ciphertext)%aes.BlockSize != 0 {
		return "", errors.New("tc 密文长度不是 AES 块大小的整数倍")
	}

	key, iv := deriveTcKeyAndIV(randomBytes, encType)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)
	plain = pkcs7Unpad(plain)

	if len(plain) < tcHashSize {
		return "", fmt.Errorf("tc 明文过短: %d", len(plain))
	}
	storedHash := plain[:tcHashSize]
	payload := plain[tcHashSize:]
	computed := sha512.Sum512(payload)
	if !bytes.Equal(storedHash, computed[:]) {
		return "", errors.New("tc 校验失败：数据损坏或密钥错误")
	}
	return string(payload), nil
}

// decryptTcStorageValue 解密 storage.json 中 base64 编码的 tc 值。
func decryptTcStorageValue(raw string) (string, error) {
	compact := strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t', ' ':
			return -1
		}
		return r
	}, raw)
	buf, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		buf, err = base64.RawStdEncoding.DecodeString(compact)
		if err != nil {
			return "", err
		}
	}
	return decryptTcBuffer(buf)
}

// pkcs7Unpad 去掉 Node crypto 默认添加/自动移除的 PKCS#7 填充。
// 若尾部不是合法填充，则按无填充处理（兼容非标准写入方）。
func pkcs7Unpad(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(data) {
		return data
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return data
		}
	}
	return data[:len(data)-pad]
}
