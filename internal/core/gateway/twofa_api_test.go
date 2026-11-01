package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
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
