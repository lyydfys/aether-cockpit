package gateway

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// sealBackupV1Sample 按旧格式造一个样本：AETHER1 | salt(16) | nonce(12) | 密文，
// 固定 600000 次迭代、无 AAD。用来锁住"旧备份仍可读"的兼容承诺。
func sealBackupV1Sample(t *testing.T, plain []byte, pass string) []byte {
	t.Helper()
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(pbkdf2SHA256([]byte(pass), salt, sealKDFIters, 32))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	out := append([]byte(sealMagicV1), salt...)
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plain, nil)
}

// TestBackupFormatV2Layout 锁住 AETHER2 的头部布局：
// magic(7) | ver(u8) | iters(u32 BE) | saltLen(u8) | salt | nonceLen(u8) | nonce | 密文。
func TestBackupFormatV2Layout(t *testing.T) {
	plain := []byte(`{"hello":"world"}`)
	sealed, err := sealBackup(plain, "correct horse battery")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if got := string(sealed[:len(sealMagicV2)]); got != "AETHER2" {
		t.Fatalf("魔数 = %q，期望 AETHER2", got)
	}
	if ver := sealed[len(sealMagicV2)]; ver != sealFormatV2 {
		t.Fatalf("ver = %d，期望 %d", ver, sealFormatV2)
	}
	iters := binary.BigEndian.Uint32(sealed[len(sealMagicV2)+1 : len(sealMagicV2)+5])
	if int(iters) != sealKDFIters {
		t.Fatalf("默认迭代数 = %d，期望 %d", iters, sealKDFIters)
	}
	pos := len(sealMagicV2) + 5
	saltLen := int(sealed[pos])
	pos++
	if saltLen != sealSaltLenV2 {
		t.Fatalf("saltLen = %d，期望 %d", saltLen, sealSaltLenV2)
	}
	pos += saltLen
	nonceLen := int(sealed[pos])
	pos++
	if nonceLen != 12 {
		t.Fatalf("nonceLen = %d，期望 12", nonceLen)
	}
	ctStart := pos + nonceLen
	if got := len(sealed) - ctStart; got != len(plain)+16 {
		t.Fatalf("密文长度 = %d，期望 %d（明文 + GCM tag）", got, len(plain)+16)
	}

	got, err := openBackup(sealed, "correct horse battery")
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("open = %q, %v", got, err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("密文里不应出现明文")
	}
}

// TestOpenBackupRejectsPlaintext 锁住 fail-closed：非 AETHER2/AETHER1 的输入一律报错，
// 绝不原样返回——否则能写同步目标的人就能让恢复侧接受任意明文 JSON。
func TestOpenBackupRejectsPlaintext(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"json object", []byte(`{"version":1,"settings":{"model_prices":{"m":1}}}`), "明文"},
		{"json array", []byte(`[{"a":1}]`), "明文"},
		{"empty", nil, "为空"},
		{"garbage", []byte{0x00, 0x01, 0x02, 0x03}, "文件头"},
		{"text", []byte("not a backup at all"), "文件头"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := openBackup(c.data, "")
			if err == nil {
				t.Fatalf("必须拒绝，却返回了 %q", got)
			}
			if got != nil {
				t.Fatalf("拒绝时不得返回任何内容: %q", got)
			}
			if !strings.Contains(err.Error(), "不是 Aether 备份文件") {
				t.Fatalf("错误文案应让用户看懂这不是备份文件: %v", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误文案应包含 %q: %v", c.want, err)
			}
		})
	}
	// 拒绝时不能把入参内容拼进错误（可能是用户的设置/凭证）。
	secret := []byte(`{"api_key":"sk-super-secret-value"}`)
	if _, err := openBackup(secret, ""); err == nil || strings.Contains(err.Error(), "sk-super-secret-value") {
		t.Fatalf("拒绝信息不得回显内容: %v", err)
	}
}

// TestOpenBackupAETHER1LegacyStillReadable 锁住旧格式只读兼容：
// 老的 AETHER1 备份换机后仍要能恢复，但写入侧必须已经改成 AETHER2。
func TestOpenBackupAETHER1LegacyStillReadable(t *testing.T) {
	plain := []byte(`{"legacy":true}`)
	legacy := sealBackupV1Sample(t, plain, "old passphrase")
	if !bytes.HasPrefix(legacy, []byte(sealMagicV1)) {
		t.Fatal("样本应以 AETHER1 开头")
	}
	if !sealedBackup(legacy) {
		t.Fatal("旧格式应仍被识别为口令密封的备份")
	}
	got, err := openBackup(legacy, "old passphrase")
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("旧备份应可解开: %q, %v", got, err)
	}
	if _, err := openBackup(legacy, "wrong passphrase"); err == nil {
		t.Fatal("旧格式的错误口令也必须失败")
	}
	// 新写入不得再产生 AETHER1。
	fresh, err := sealBackup(plain, "new passphrase")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.HasPrefix(fresh, []byte(sealMagicV1)) {
		t.Fatal("写入侧仍在使用 AETHER1，应升级为 AETHER2")
	}
}

// TestOpenBackupRejectsTamperedIters 锁住迭代数篡改：
// 越界（降级 / 拖死 CPU）在派生密钥前就被拒；区间内被改则由 AAD + 密钥派生
// 让完整性校验失败。
func TestOpenBackupRejectsTamperedIters(t *testing.T) {
	const pass = "tamper check pass"
	plain := []byte(`{"prices":{"m":1}}`)
	sealed, err := sealBackup(plain, pass)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	itersAt := len(sealMagicV2) + 1

	tamper := func(iters uint32) []byte {
		cp := append([]byte(nil), sealed...)
		binary.BigEndian.PutUint32(cp[itersAt:itersAt+4], iters)
		return cp
	}

	// 低于下界：典型降级攻击，必须在派生密钥前拒绝。
	low := tamper(sealMinIters - 1)
	if got, err := openBackup(low, pass); err == nil || !strings.Contains(err.Error(), "迭代数") {
		t.Fatalf("低于下界的迭代数应被拒: %q, %v", got, err)
	}
	// 高于上界：恢复时会被拖死，同样拒绝。
	high := tamper(sealMaxIters + 1)
	if got, err := openBackup(high, pass); err == nil || !strings.Contains(err.Error(), "迭代数") {
		t.Fatalf("高于上界的迭代数应被拒: %q, %v", got, err)
	}
	// 区间内但与原值不同：结构合法，但 AAD/密钥都变了，完整性校验必须失败。
	mid := tamper(300000)
	if got, err := openBackup(mid, pass); err == nil {
		t.Fatalf("篡改迭代数后仍能解开，说明 iters 没有参与完整性校验: %q", got)
	} else if strings.Contains(err.Error(), "迭代数") {
		t.Fatalf("区间内的迭代数不应被判为区间错误: %v", err)
	}
	// 原文件不受影响。
	got, err := openBackup(sealed, pass)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("原备份应可解开: %q, %v", got, err)
	}
}

// TestOpenBackupRejectsUnknownVersion 锁住未知版本：未来格式不能被当成可读。
func TestOpenBackupRejectsUnknownVersion(t *testing.T) {
	sealed, err := sealBackup([]byte(`{"v":1}`), "version check")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	bad := append([]byte(nil), sealed...)
	bad[len(sealMagicV2)] = 2
	if got, err := openBackup(bad, "version check"); err == nil || !strings.Contains(err.Error(), "版本") {
		t.Fatalf("未知版本应被拒: %q, %v", got, err)
	}
}

// TestDavSyncSaveRejectsPassphraseReuse 锁住口令复用：备份口令不得等于远端存储凭证。
func TestDavSyncSaveRejectsPassphraseReuse(t *testing.T) {
	const secret = "same-secret-value"
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			"webdav",
			`{"kind":"webdav","url":"https://dav.example.com/dav/backup/","user":"u","password":"` + secret + `","passphrase":"` + secret + `"}`,
			"WebDAV 密码",
		},
		{
			"s3",
			`{"kind":"s3","url":"s3://bucket/backups","user":"AK","password":"` + secret + `","passphrase":"` + secret + `"}`,
			"S3 Secret",
		},
		{
			"github",
			`{"kind":"github","url":"github://owner/repo/dir","password":"` + secret + `","passphrase":"` + secret + `"}`,
			"GitHub Token",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
			rec := httptest.NewRecorder()
			g.handleDavSyncSave(rec, httptest.NewRequest("POST", "/api/davsync/save", strings.NewReader(c.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("口令复用应 400，得到 %d %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, c.want) || !strings.Contains(body, "独立的秘密") {
				t.Fatalf("错误文案应指明冲突对象与理由: %s", body)
			}
			if strings.Contains(body, secret) {
				t.Fatalf("错误响应回显了凭证: %s", body)
			}
			if g.settings.Sync != nil {
				t.Fatal("冲突的同步配置不应写入 settings")
			}
		})
	}

	// 对照：口令与凭证不同时必须照常保存（不能误伤正常配置）。
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	rec := httptest.NewRecorder()
	okBody := `{"kind":"s3","url":"s3://bucket/backups","user":"AK","password":"` + secret + `","passphrase":"independent-backup-pass"}`
	g.handleDavSyncSave(rec, httptest.NewRequest("POST", "/api/davsync/save", strings.NewReader(okBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("口令独立时应保存成功，得到 %d %s", rec.Code, rec.Body.String())
	}
	if g.settings.Sync == nil || g.settings.Sync.PassphraseEnc == "" {
		t.Fatal("口令独立时必须落盘 PassphraseEnc")
	}
}
