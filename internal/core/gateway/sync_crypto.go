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
	"fmt"
	"io"

	"aether-cockpit/internal/core/simplecrypt"
)

// 口令密封的配置备份。
//
// aether 的同步文件用口令密封，换一台电脑输入同一口令即可还原；
// 口令本身用本机绑定的 simplecrypt 保存，GET 时永远不回显。
//
// 当前写入格式（AETHER2，ver=1）：
//
//	magic(7B "AETHER2") | ver(u8=1) | iters(u32 BE) | saltLen(u8) | salt | nonceLen(u8) | nonce | AES-256-GCM 密文
//
// 为什么要把迭代数写进头部：
//  1. 以后要提高 KDF 强度时不必再改格式——读侧按文件里声明的迭代数解密，老文件
//     继续用老迭代数打开，新文件用新迭代数写入；
//  2. 读侧可以对迭代数做区间校验（sealMinIters/sealMaxIters）：攻击者把它改小是
//     降级攻击（口令被削弱），改大则是拒绝服务（恢复时 CPU 空转几小时），两头都
//     必须拒绝。
//
// 头部（magic..nonce 整段）作为 GCM 的 AAD，所以 iters 哪怕只改一个字节也过不了
// 完整性校验；这比"只让 iters 参与派生密钥"更直接。
//
// 兼容策略：AETHER1（无 ver、无 iters，固定 600000 次）只读不写——它无法表达迭代
// 数，想升级 KDF 就必须破兼容，所以新备份一律写 AETHER2。除这两种魔数外的任何
// 输入都不是备份，读取侧必须报错（fail-closed），原因见 openBackup。
const (
	// sealMagicV2 是当前写入格式的魔数。
	sealMagicV2 = "AETHER2"
	// sealMagicV1 是旧格式魔数，只用于兼容读取与"这是不是备份"的判定。
	sealMagicV1 = "AETHER1"

	// sealFormatV2 是 AETHER2 的头部版本号；不认识的版本一律拒收。
	sealFormatV2 = 1

	// sealKDFIters 是新备份的默认 PBKDF2 迭代数；AETHER1 读取时也固定用它。
	sealKDFIters = 600000
	// sealMinIters/sealMaxIters 是读侧接受的迭代数区间：下界防降级，上界防
	// "改成天文数字把恢复拖死"的拒绝服务。写入侧同样不允许越界。
	sealMinIters = 100000
	sealMaxIters = 10000000

	// sealSaltLenV2 是写入侧的 salt 长度；读取侧接受 [sealMinSaltLen, sealMaxSaltLen]，
	// 但至少 16 字节，过短的 salt 会让同一口令在不同备份间复用预计算表。
	sealSaltLenV2  = 16
	sealMinSaltLen = 16
	sealMaxSaltLen = 64

	// sealNonceLen 是 GCM 的 nonce 长度（标准 96 位）。读取侧先校验再派生密钥，
	// 免得不合法的头部也要空跑一次 KDF。
	sealNonceLen = 12
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

// sealBackup 用口令加密备份。云同步/导出必须带口令：
// 明文上传会把设置、Library、Spec 直接暴露给远端存储。
func sealBackup(plain []byte, passphrase string) ([]byte, error) {
	return sealBackupWithIters(plain, passphrase, sealKDFIters)
}

// sealBackupWithIters 允许指定迭代数（测试与将来的强度升级用它）。
func sealBackupWithIters(plain []byte, passphrase string, iters int) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("备份必须设置口令；明文备份不再允许上传")
	}
	if iters < sealMinIters || iters > sealMaxIters {
		// 写入侧也拒收越界迭代数：读侧一定会拒收，写出来只会得到一份打不开的备份。
		return nil, fmt.Errorf("备份迭代数 %d 超出允许区间 [%d, %d]", iters, sealMinIters, sealMaxIters)
	}
	salt := make([]byte, sealSaltLenV2)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	key := pbkdf2SHA256([]byte(passphrase), salt, iters, 32)
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

	header := make([]byte, 0, len(sealMagicV2)+1+4+1+len(salt)+1+len(nonce))
	header = append(header, sealMagicV2...)
	header = append(header, byte(sealFormatV2))
	var itersBuf [4]byte
	binary.BigEndian.PutUint32(itersBuf[:], uint32(iters))
	header = append(header, itersBuf[:]...)
	header = append(header, byte(len(salt)))
	header = append(header, salt...)
	header = append(header, byte(len(nonce)))
	header = append(header, nonce...)

	// AAD 就是这段头部：读侧必须原样重建同一段头部（含 iters）才能通过校验。
	body := gcm.Seal(nil, nonce, plain, header)
	out := make([]byte, 0, len(header)+len(body))
	out = append(out, header...)
	out = append(out, body...)
	return out, nil
}

// openBackup 解密备份。
//
// 读侧按魔数分派，**未识别的一律报错，绝不把入参原样返回**。这是安全边界：
// 云同步目标（WebDAV / S3 / GitHub）是共享存储，能往那里写文件的人远比能改本机
// 设置的人多；如果恢复流程愿意"没魔数就当明文 JSON 用"，那么任何能写同步目标的
// 人（或能改共享仓库的人）都能让网关恢复出一份精心构造的设置/账号标签。
// 所以：明文备份不再被接受，这属于 fail-closed 的有意取舍，不提供兼容开关。
func openBackup(data []byte, passphrase string) ([]byte, error) {
	switch {
	case bytes.HasPrefix(data, []byte(sealMagicV2)):
		return openBackupV2(data, passphrase)
	case bytes.HasPrefix(data, []byte(sealMagicV1)):
		return openBackupV1(data, passphrase)
	}
	return nil, errNotBackup(data)
}

// errNotBackup 给"不是备份文件"一个用户能看懂的原因。
// 注意只按首字节分类，绝不把入参内容拼进错误里：它可能是用户的设置或凭证。
func errNotBackup(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	switch {
	case len(trimmed) == 0:
		return errors.New("这不是 Aether 备份文件：内容为空。请选择用口令导出的备份（.aether-backup）")
	case trimmed[0] == '{' || trimmed[0] == '[':
		return errors.New("这不是 Aether 备份文件：内容是未加密的明文 JSON，已拒绝导入。请改用带口令导出的备份")
	}
	return errors.New("这不是 Aether 备份文件：文件头不是 AETHER2 / AETHER1，可能选错了文件或文件已损坏")
}

// openBackupV2 读取当前格式。
func openBackupV2(data []byte, passphrase string) ([]byte, error) {
	const fixed = len(sealMagicV2) + 1 + 4 + 1 // magic + ver + iters + saltLen
	if len(data) < fixed+1 {
		return nil, errors.New("备份文件损坏：头部不完整")
	}
	if ver := data[len(sealMagicV2)]; ver != sealFormatV2 {
		return nil, fmt.Errorf("不支持的备份格式版本 %d：可能来自更新的客户端，请升级后再恢复", ver)
	}
	iters := int(binary.BigEndian.Uint32(data[len(sealMagicV2)+1 : len(sealMagicV2)+5]))
	if iters < sealMinIters || iters > sealMaxIters {
		// 这里在派生密钥之前就拒绝：既防降级，也防"迭代数被改成天文数字拖死 CPU"。
		return nil, fmt.Errorf("备份文件被篡改：迭代数 %d 不在允许区间 [%d, %d] 内", iters, sealMinIters, sealMaxIters)
	}
	pos := len(sealMagicV2) + 5
	saltLen := int(data[pos])
	pos++
	if saltLen < sealMinSaltLen || saltLen > sealMaxSaltLen {
		return nil, fmt.Errorf("备份文件损坏：salt 长度 %d 不合法", saltLen)
	}
	if len(data) < pos+saltLen+1 {
		return nil, errors.New("备份文件损坏：头部不完整")
	}
	salt := data[pos : pos+saltLen]
	pos += saltLen
	nonceLen := int(data[pos])
	pos++
	if nonceLen != sealNonceLen {
		return nil, fmt.Errorf("备份文件损坏：nonce 长度 %d 不合法", nonceLen)
	}
	if len(data) < pos+nonceLen {
		return nil, errors.New("备份文件损坏：头部不完整")
	}
	nonce := data[pos : pos+nonceLen]
	pos += nonceLen
	ct := data[pos:]
	if len(ct) == 0 {
		return nil, errors.New("备份文件损坏：缺少密文")
	}
	if passphrase == "" {
		return nil, errors.New("备份已用口令加密：请先在同步设置里填入口令")
	}
	block, err := aes.NewCipher(pbkdf2SHA256([]byte(passphrase), salt, iters, 32))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ct, data[:pos])
	if err != nil {
		return nil, errors.New("口令不对或备份被篡改：无法解开这份备份")
	}
	return plain, nil
}

// openBackupV1 读旧格式：magic(7) | salt(16) | nonce(12) | 密文，固定 600000 次迭代，
// 没有 AAD。只读不写：它表达不了迭代数，所以新备份一律 AETHER2。
func openBackupV1(data []byte, passphrase string) ([]byte, error) {
	const saltLen, nonceLen = 16, 12
	rest := data[len(sealMagicV1):]
	if len(rest) < saltLen+nonceLen+1 {
		return nil, errors.New("备份文件损坏：头部不完整")
	}
	if passphrase == "" {
		return nil, errors.New("备份已用口令加密：请先在同步设置里填入口令")
	}
	salt := rest[:saltLen]
	nonce := rest[saltLen : saltLen+nonceLen]
	ct := rest[saltLen+nonceLen:]
	block, err := aes.NewCipher(pbkdf2SHA256([]byte(passphrase), salt, sealKDFIters, 32))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errors.New("口令不对或备份已损坏：无法解开这份备份")
	}
	return plain, nil
}

// sealedBackup 判断"这是不是口令密封的备份"（新旧两种魔数都算）。
func sealedBackup(data []byte) bool {
	return bytes.HasPrefix(data, []byte(sealMagicV2)) || bytes.HasPrefix(data, []byte(sealMagicV1))
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
