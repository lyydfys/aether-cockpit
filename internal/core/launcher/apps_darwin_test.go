//go:build darwin

package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinAppExecutable(t *testing.T) {
	app := filepath.Join(t.TempDir(), "Test.app")
	exe := filepath.Join(app, "Contents", "MacOS", "Test")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := darwinAppExecutable(app); got != exe {
		t.Fatalf("exe = %q, want %q", got, exe)
	}
	if got := darwinAppExecutable(filepath.Join(t.TempDir(), "Missing.app")); got != "" {
		t.Fatalf("缺失 bundle 应返回空，得到 %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Fatalf("shellQuote = %q", got)
	}
	if validEnvKey("PATH") != true || validEnvKey("A-B") != false || validEnvKey("") != false {
		t.Fatal("env key validation 不符")
	}
}
