package ir

import (
	"errors"
	"strings"
	"testing"
)

func TestPublicMessageAddsCauseForUpstream(t *testing.T) {
	ae := NewAPIError(ErrCodeUpstreamFailed, "上游连接失败").
		WithCause(errors.New("Post \"https://example.com\": dial tcp: i/o timeout"))
	got := ae.PublicMessage()
	if !strings.Contains(got, "i/o timeout") || !strings.HasPrefix(got, "上游连接失败：") {
		t.Fatalf("PublicMessage = %q", got)
	}

	// 鉴权错误不附带 cause（可能含内部细节）。
	auth := NewAPIError(ErrCodeUpstreamAuth, "鉴权失败").WithCause(errors.New("token=secret"))
	if got := auth.PublicMessage(); strings.Contains(got, "secret") {
		t.Fatalf("鉴权错误泄露 cause: %q", got)
	}

	// 长原因截断，且压成单行。
	long := NewAPIError(ErrCodeUpstreamFailed, "失败").WithCause(errors.New(strings.Repeat("x", 500) + "\nnext"))
	got = long.PublicMessage()
	if strings.Contains(got, "\n") {
		t.Fatal("应压成单行")
	}
	if n := len([]rune(got)); n > len([]rune("失败："))+181 {
		t.Fatalf("未截断: %d", n)
	}
}
