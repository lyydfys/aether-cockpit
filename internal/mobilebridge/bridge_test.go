package mobilebridge

import "testing"

func TestABIVersionIsPinned(t *testing.T) {
	if ABIVersion != "mobilebridge/2" {
		t.Fatalf("ABIVersion = %q, want mobilebridge/2", ABIVersion)
	}
}

func TestWriteABIVersion(t *testing.T) {
	buf := make([]byte, 32)
	if got, want := WriteABIVersion(buf), len(ABIVersion)+1; got != want {
		t.Fatalf("WriteABIVersion = %d, want %d", got, want)
	}
	if got := string(buf[:len(ABIVersion)]); got != ABIVersion {
		t.Fatalf("buffer = %q, want %q", got, ABIVersion)
	}
	if buf[len(ABIVersion)] != 0 {
		t.Fatal("version is not NUL-terminated")
	}

	if got, want := WriteABIVersion(buf[:4]), -(len(ABIVersion) + 1); got != want {
		t.Fatalf("small buffer = %d, want %d", got, want)
	}
}
