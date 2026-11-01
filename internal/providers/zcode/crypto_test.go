package zcode

import (
	"strings"
	"testing"
)

// TestDecryptOfficialVector 用官方算法（Node crypto，固定 IV/secret）生成的
// 密文做交叉验证，确保 Go 端实现与 ZCode 客户端一致。
func TestDecryptOfficialVector(t *testing.T) {
	t.Setenv("ZCODE_CREDENTIAL_SECRET", "test-secret")
	vector := "enc:v1:BwcHBwcHBwcHBwcH.zf7LAoDqqoM_ARmccsPCMw.xiXxk1k_KswiiGU"
	plain, err := decryptCredential(vector)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plain != "sk-live-abc" {
		t.Fatalf("plain = %q", plain)
	}

	sealed, err := encryptCredential("sk-live-abc")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(sealed, "enc:v1:") {
		t.Fatalf("信封前缀错误: %q", sealed)
	}
	back, err := decryptCredential(sealed)
	if err != nil || back != "sk-live-abc" {
		t.Fatalf("round trip = %q err=%v", back, err)
	}

	// 换 secret 后必须解不开（换机器即失效的语义）。
	t.Setenv("ZCODE_CREDENTIAL_SECRET", "another-secret")
	if _, err := decryptCredential(vector); err == nil {
		t.Fatal("不同 secret 不应解出明文")
	}
	// 明文值原样返回。
	if v, err := decryptCredential("plain-key"); err != nil || v != "plain-key" {
		t.Fatalf("明文透传失败: %q %v", v, err)
	}
}
