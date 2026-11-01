package totp

import (
	"encoding/base32"
	"testing"
	"time"
)

func b32(s string) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(s))
}

// RFC 6238 附录 B 的标准向量（8 位码）。
func TestCodeRFC6238Vectors(t *testing.T) {
	sec20 := b32("12345678901234567890")
	sec32 := b32("12345678901234567890123456789012")
	sec64 := b32("1234567890123456789012345678901234567890123456789012345678901234")
	cases := []struct {
		name string
		sec  string
		unix int64
		algo string
		want string
	}{
		{"sha1-59", sec20, 59, "SHA1", "94287082"},
		{"sha1-1111111109", sec20, 1111111109, "SHA1", "07081804"},
		{"sha1-1111111111", sec20, 1111111111, "SHA1", "14050471"},
		{"sha1-1234567890", sec20, 1234567890, "SHA1", "89005924"},
		{"sha256-59", sec32, 59, "SHA256", "46119246"},
		{"sha256-1111111109", sec32, 1111111109, "SHA256", "68084774"},
		{"sha512-59", sec64, 59, "SHA512", "90693936"},
		{"sha512-1111111109", sec64, 1111111109, "SHA512", "25091201"},
	}
	for _, c := range cases {
		got, remaining, err := Code(c.sec, time.Unix(c.unix, 0), 8, 30, c.algo)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s: code = %s, want %s", c.name, got, c.want)
		}
		if remaining < 1 || remaining > 30 {
			t.Fatalf("%s: remaining = %d", c.name, remaining)
		}
	}
}

func TestCodeDefaultsAndSpacing(t *testing.T) {
	sec := b32("12345678901234567890")
	spaced := " " + lower(sec[:4]) + " " + lower(sec[4:]) + " ="
	got, remaining, err := Code(spaced, time.Unix(59, 0), 0, 0, "")
	if err != nil {
		t.Fatalf("大小写/空格密钥应可解码: %v", err)
	}
	if len(got) != 6 {
		t.Fatalf("默认位数 = %d, want 6", len(got))
	}
	if remaining != 1 {
		t.Fatalf("t=59 剩余 = %d, want 1", remaining)
	}
}

func TestValidateRejectsBadSecret(t *testing.T) {
	if err := Validate("not-base32!!"); err == nil {
		t.Fatal("非法 base32 应报错")
	}
	if err := Validate(""); err == nil {
		t.Fatal("空密钥应报错")
	}
	if _, _, err := Code("JBSWY3DPEHPK3PXP", time.Now(), 6, 30, "MD5"); err == nil {
		t.Fatal("不支持的算法应报错")
	}
}

func lower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + 32
		}
	}
	return string(out)
}
