package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/core/credential"
	"aether-cockpit/internal/core/totp"
)

// TestTwoFASecretLifecycle 覆盖 2FA 库：添加 → 列表不泄密 → 本机算码 → 删除。
func TestTwoFASecretLifecycle(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		SettingsPath:     filepath.Join(dir, "settings.json"),
	})
	secret := "JBSWY3DPEHPK3PXP"

	post := httptest.NewRequest(http.MethodPost, "/api/2fa", strings.NewReader(
		`{"issuer":"GitHub","account":"dev@example.com","note":"主账号","secret":"`+secret+`","digits":6,"period":30,"algorithm":"SHA1"}`))
	rec := httptest.NewRecorder()
	g.handle2FAAdd(rec, post)
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatal("添加响应泄露了明文密钥")
	}
	var added struct {
		Entry struct {
			ID string `json:"id"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil || added.Entry.ID == "" {
		t.Fatalf("解析添加结果失败: %v %s", err, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	g.handle2FAList(rec, httptest.NewRequest(http.MethodGet, "/api/2fa", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, secret) || strings.Contains(body, "secret_enc") {
		t.Fatalf("列表泄露密钥: %s", body)
	}
	var list struct {
		Entries []struct {
			ID         string `json:"id"`
			Issuer     string `json:"issuer"`
			SecretTail string `json:"secret_tail"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Entries) != 1 {
		t.Fatalf("列表解析失败: %v %s", err, body)
	}
	if list.Entries[0].Issuer != "GitHub" || !strings.HasSuffix(secret, list.Entries[0].SecretTail) {
		t.Fatalf("条目内容不对: %+v", list.Entries[0])
	}

	rec = httptest.NewRecorder()
	g.handle2FACodes(rec, httptest.NewRequest(http.MethodGet, "/api/2fa/codes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("codes status = %d", rec.Code)
	}
	var codes struct {
		Entries []struct {
			Code      string `json:"code"`
			Remaining int    `json:"remaining"`
			Error     string `json:"error"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &codes); err != nil || len(codes.Entries) != 1 {
		t.Fatalf("codes 解析失败: %v %s", err, rec.Body.String())
	}
	one := codes.Entries[0]
	if one.Error != "" || len(one.Code) != 6 || one.Remaining < 1 || one.Remaining > 30 {
		t.Fatalf("验证码不对: %+v", one)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/2fa/"+added.Entry.ID, nil)
	req.SetPathValue("id", added.Entry.ID)
	rec = httptest.NewRecorder()
	g.handle2FADelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	g.handle2FAList(rec, httptest.NewRequest(http.MethodGet, "/api/2fa", nil))
	if !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Fatalf("删除后列表未清空: %s", rec.Body.String())
	}
}

// TestTwoFARejectsBadSecret 非法 base32 / 不支持算法必须 400。
func TestTwoFARejectsBadSecret(t *testing.T) {
	dir := t.TempDir()
	g := New(Config{Addr: "127.0.0.1:0", UsagePath: "-", AccountStorePath: filepath.Join(dir, "accounts.json"), SettingsPath: filepath.Join(dir, "settings.json")})
	for _, body := range []string{
		`{"issuer":"x","secret":"not-base32!!"}`,
		`{"issuer":"x","secret":"JBSWY3DPEHPK3PXP","algorithm":"MD5"}`,
	} {
		rec := httptest.NewRecorder()
		g.handle2FAAdd(rec, httptest.NewRequest(http.MethodPost, "/api/2fa", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d", body, rec.Code)
		}
	}
}

// twoFATestGateway 起一个只带本地路径的网关；credentialKey 为空时保持桌面兼容路径。
func twoFATestGateway(t *testing.T, credentialKey []byte) *Gateway {
	t.Helper()
	dir := t.TempDir()
	return New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		SettingsPath:     filepath.Join(dir, "settings.json"),
		CredentialKey:    credentialKey,
	})
}

// addTwoFAEntry 走真实 handler 添加一条，返回条目 id。
func addTwoFAEntry(t *testing.T, g *Gateway, secret string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handle2FAAdd(rec, httptest.NewRequest(http.MethodPost, "/api/2fa", strings.NewReader(
		`{"issuer":"GitHub","account":"dev","secret":"`+secret+`","digits":6,"period":30,"algorithm":"SHA1"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("add status = %d body=%s", rec.Code, rec.Body.String())
	}
	var added struct {
		Entry struct {
			ID string `json:"id"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil || added.Entry.ID == "" {
		t.Fatalf("解析添加结果失败: %v %s", err, rec.Body.String())
	}
	return added.Entry.ID
}

// storedTwoFASecret 读回落盘的 secret_enc 原始字节。
func storedTwoFASecret(t *testing.T, g *Gateway) []byte {
	t.Helper()
	raw, err := os.ReadFile(g.twoFAPath())
	if err != nil {
		t.Fatalf("读 2fa.json 失败: %v", err)
	}
	var f twoFAFile
	if err := json.Unmarshal(raw, &f); err != nil || len(f.Entries) != 1 {
		t.Fatalf("解析 2fa.json 失败: %v entries=%d", err, len(f.Entries))
	}
	enc, err := base64.StdEncoding.DecodeString(f.Entries[0].SecretEnc)
	if err != nil {
		t.Fatalf("secret_enc 不是合法 base64: %v", err)
	}
	return enc
}

// codesFor 取当前验证码；解不开时返回 error 文案。
func codesFor(t *testing.T, g *Gateway) (string, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	g.handle2FACodes(rec, httptest.NewRequest(http.MethodGet, "/api/2fa/codes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("codes status = %d body=%s", rec.Code, rec.Body.String())
	}
	var codes struct {
		Entries []struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &codes); err != nil || len(codes.Entries) != 1 {
		t.Fatalf("codes 解析失败: %v %s", err, rec.Body.String())
	}
	return codes.Entries[0].Code, codes.Entries[0].Error
}

// TestTwoFASecretUsesCredentialEnvelopeWhenKeyIsSet 是移动端的前置条件：
// 给了 CredentialKey（Android 由 Keystore 派生）时，TOTP seed 必须走 envelope v2
// （AES-GCM，AAD 绑定条目 ID），而不是 simplecrypt——后者的 key 来自 hostname/home，
// 在 Android 上要么退化成常量 seed（设备间同 key），要么换装即解不开。
func TestTwoFASecretUsesCredentialEnvelopeWhenKeyIsSet(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, credential.KeySize)
	g := twoFATestGateway(t, key)
	secret := "JBSWY3DPEHPK3PXP"
	id := addTwoFAEntry(t, g, secret)

	stored := storedTwoFASecret(t, g)
	if !credential.IsV2(stored) {
		head := stored
		if len(head) > 4 {
			head = head[:4]
		}
		t.Fatalf("设了 CredentialKey 时 seed 必须用 envelope v2，实际前 4 字节 = %q", head)
	}

	// AAD 绑定：用本条目 ID 能解开，换成别的 ID 必须解不开。
	plain, err := credential.Open(key, stored, credential.AAD("2fa", id))
	if err != nil {
		t.Fatalf("用条目 ID 解封失败: %v", err)
	}
	if string(plain) != secret {
		t.Fatalf("解封出 %q，期望 %q", string(plain), secret)
	}
	if _, err := credential.Open(key, stored, credential.AAD("2fa", id+"-other")); err == nil {
		t.Fatal("换成别的条目 ID 仍然解得开：AAD 没绑定条目 ID")
	}

	// 端到端：仍然要能算出与独立实现一致的 6 位码。
	code, codeErr := codesFor(t, g)
	if codeErr != "" {
		t.Fatalf("出码失败: %s", codeErr)
	}
	want, _, err := totp.Code(secret, time.Now(), 6, 30, "SHA1")
	if err != nil {
		t.Fatalf("独立算码失败: %v", err)
	}
	if code != want {
		t.Fatalf("码不一致: handler=%s 独立=%s", code, want)
	}
}

// TestTwoFASecretKeepsSimplecryptWithoutKey 保证桌面兼容：没有 CredentialKey 时
// 仍写 simplecrypt 老格式（既有 2fa.json 继续可读）。
func TestTwoFASecretKeepsSimplecryptWithoutKey(t *testing.T) {
	g := twoFATestGateway(t, nil)
	addTwoFAEntry(t, g, "JBSWY3DPEHPK3PXP")

	if credential.IsV2(storedTwoFASecret(t, g)) {
		t.Fatal("没有 CredentialKey 时不应该写 envelope v2（会让桌面读不了）")
	}
	code, codeErr := codesFor(t, g)
	if codeErr != "" || len(code) != 6 {
		t.Fatalf("老格式仍应能出码: code=%q err=%q", code, codeErr)
	}
}

// TestTwoFAEnvelopeWithoutKeyReportsHonestError 有 v2 密文但没给 key 时，
// 必须如实报「无法解密」，不能假装成功或返回空码。
func TestTwoFAEnvelopeWithoutKeyReportsHonestError(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, credential.KeySize)
	sealed := twoFATestGateway(t, key)
	addTwoFAEntry(t, sealed, "JBSWY3DPEHPK3PXP")
	raw, err := os.ReadFile(sealed.twoFAPath())
	if err != nil {
		t.Fatalf("读 2fa.json 失败: %v", err)
	}

	// 把同一份 2fa.json 搬到另一个没有 CredentialKey 的网关目录下。
	dir := t.TempDir()
	plainGW := New(Config{
		Addr:             "127.0.0.1:0",
		UsagePath:        "-",
		AccountStorePath: filepath.Join(dir, "accounts.json"),
		SettingsPath:     filepath.Join(dir, "settings.json"),
	})
	if err := os.WriteFile(plainGW.twoFAPath(), raw, 0o600); err != nil {
		t.Fatalf("复制 2fa.json 失败: %v", err)
	}
	code, codeErr := codesFor(t, plainGW)
	if codeErr == "" {
		t.Fatalf("没有 key 却解开了 v2 密文（code=%q），这是假成功", code)
	}
	if code != "" {
		t.Fatalf("失败时不应返回验证码，实际 %q", code)
	}
}
