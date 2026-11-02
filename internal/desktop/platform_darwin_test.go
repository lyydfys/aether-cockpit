//go:build darwin

package desktop

import (
	"path/filepath"
	"testing"
)

func TestMergeSearchPath(t *testing.T) {
	got := mergeSearchPath("/usr/bin:/bin", "/opt/homebrew/bin:/usr/bin")
	want := "/opt/homebrew/bin" + string(filepath.ListSeparator) + "/usr/bin" + string(filepath.ListSeparator) + "/bin"
	if got != want {
		t.Fatalf("mergeSearchPath = %q, want %q", got, want)
	}
}

func TestLastNonEmptyLine(t *testing.T) {
	if got := lastNonEmptyLine("banner\n/usr/local/bin:/usr/bin\n\n"); got != "/usr/local/bin:/usr/bin" {
		t.Fatalf("got %q", got)
	}
	if got := lastNonEmptyLine(""); got != "" {
		t.Fatalf("空输入应返回空，得到 %q", got)
	}
}
