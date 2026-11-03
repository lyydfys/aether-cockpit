package credential

import (
	"bytes"
	"testing"
)

func TestSealOpenRoundTripBindsAAD(t *testing.T) {
	key := bytes.Repeat([]byte{7}, KeySize)
	aad := AAD("mock-native", "acct-1")
	plain := []byte(`{"api_key":"fixture-token"}`)

	sealed, err := Seal(key, plain, aad)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !IsV2(sealed) {
		t.Fatal("sealed payload is not envelope v2")
	}
	got, err := Open(key, sealed, aad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("roundtrip = %q", got)
	}
	if _, err := Open(key, sealed, AAD("mock-native", "acct-2")); err == nil {
		t.Fatal("AAD mismatch must fail")
	}
	wrong := bytes.Repeat([]byte{8}, KeySize)
	if _, err := Open(wrong, sealed, aad); err == nil {
		t.Fatal("wrong key must fail")
	}
	if IsV2([]byte("not-sealed")) {
		t.Fatal("plain payload reported as v2")
	}
	if _, err := Seal(key[:16], plain, aad); err == nil {
		t.Fatal("short key must fail")
	}
}
