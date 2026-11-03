package mobile

import (
	"encoding/base64"
	"testing"
)

func TestDecodeAccountKey(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	encoded := base64.StdEncoding.EncodeToString(key)

	got, err := decodeAccountKey(encoded)
	if err != nil {
		t.Fatalf("decode valid key: %v", err)
	}
	if string(got) != string(key) {
		t.Fatal("decoded key differs")
	}
	if got, err := decodeAccountKey("  "); err != nil || got != nil {
		t.Fatalf("blank key = %v, %v", got, err)
	}
	for _, bad := range []string{"not-base64!!", base64.StdEncoding.EncodeToString(key[:16])} {
		if _, err := decodeAccountKey(bad); err == nil {
			t.Fatalf("accepted invalid key %q", bad)
		}
	}
}
