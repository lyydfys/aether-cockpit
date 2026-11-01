package gateway

import (
	"strings"
	"testing"
)

func TestReadLimited(t *testing.T) {
	if _, err := readLimited(strings.NewReader("12345"), 4, "x"); err == nil {
		t.Fatal("超过上限应报错")
	}
	raw, err := readLimited(strings.NewReader("12345"), 5, "x")
	if err != nil || string(raw) != "12345" {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
}
