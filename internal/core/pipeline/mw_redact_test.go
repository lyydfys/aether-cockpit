package pipeline

import (
	"strings"
	"testing"

	"aether-cockpit/internal/core/redact"
)

// 占位符被上游拆成多个 chunk 时，必须跨 chunk 还原，不能把半截占位符发给用户。
func TestUnmaskChunkAcrossSplits(t *testing.T) {
	e := redact.New()
	e.SetEnabled(true)
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	masked := e.Mask("key "+secret+" end", "", "").Text
	i := strings.Index(masked, "{{")
	j := strings.Index(masked, "}}") + 2
	ph := masked[i:j]

	for cut := 1; cut < len(ph); cut++ {
		var pending string
		var got strings.Builder
		got.WriteString(unmaskChunk(e, "", &pending, masked[:i]+ph[:cut], false))
		got.WriteString(unmaskChunk(e, "", &pending, ph[cut:]+masked[j:], false))
		got.WriteString(unmaskChunk(e, "", &pending, "", true))
		if got.String() != "key "+secret+" end" {
			t.Fatalf("cut=%d → %q (pending=%q)", cut, got.String(), pending)
		}
	}
}

func TestUnmaskChunkPlainText(t *testing.T) {
	e := redact.New()
	e.SetEnabled(true)
	var pending string
	if got := unmaskChunk(e, "", &pending, "hello {{not a placeholder", false); got != "hello " || !strings.HasPrefix(pending, "{{") {
		t.Fatalf("got=%q pending=%q", got, pending)
	}
	if got := unmaskChunk(e, "", &pending, "}} world", false); got != "{{not a placeholder}} world" {
		t.Fatalf("second = %q", got)
	}
	if got := unmaskChunk(e, "", &pending, "", true); got != "" {
		t.Fatalf("final = %q", got)
	}
}
