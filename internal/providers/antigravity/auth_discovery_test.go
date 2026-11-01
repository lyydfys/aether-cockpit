package antigravity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanOAuthFile(t *testing.T) {
	id := "123456789012-" + "abcdefghijklmnopqrstuvwxyz" + ".apps.googleusercontent.com"
	secret := "GOCSPX-" + "fakeSecretValue123456"
	other := "GOCSPX-" + "anotherFakeValue654321"
	p := filepath.Join(t.TempDir(), "language_server.exe")
	body := "prefix " + id + " middle " + secret + " suffix " + other
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	pairs := scanOAuthFile(p)
	if len(pairs) != 2 {
		t.Fatalf("pairs = %d, want 2 (%v)", len(pairs), pairs)
	}
	for _, pair := range pairs {
		if pair.ID != id || !strings.HasPrefix(pair.Secret, "GOCSPX-") {
			t.Fatalf("pair = %+v", pair)
		}
	}
}
