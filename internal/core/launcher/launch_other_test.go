//go:build !windows

package launcher

import (
	"os"
	"strings"
	"testing"
)

func TestProcessAliveFailsClosedOnNonWindows(t *testing.T) {
	if processAlive(os.Getpid()) {
		t.Fatal("non-Windows processAlive must fail closed")
	}
}

func TestLaunchDesktopInstanceUnsupportedOnNonWindows(t *testing.T) {
	m := New()
	_, err := m.LaunchDesktopInstance("demo", "/does/not/exist", nil, nil, t.TempDir())
	if err == nil {
		t.Fatal("expected a Windows-only error")
	}
	if !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("err = %v", err)
	}
	if got := m.List(); len(got) != 0 {
		t.Fatalf("instances = %+v, want none", got)
	}
}
