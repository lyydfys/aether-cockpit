//go:build !windows

package workbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

func TestDiscoverLocalAccountsUnsupportedOnNonWindows(t *testing.T) {
	p := newProvider(cnProfile)
	drafts, err := p.DiscoverLocalAccounts(context.Background())
	if drafts != nil {
		t.Fatalf("drafts = %+v, want nil", drafts)
	}
	ae := ir.AsAPIError(err)
	if ae == nil || ae.Code != ir.ErrCodeUnsupported {
		t.Fatalf("err = %v, want %s", err, ir.ErrCodeUnsupported)
	}
	if !strings.Contains(ae.Message, "Windows") || !strings.Contains(ae.Message, "手动") {
		t.Fatalf("message = %q", ae.Message)
	}
}

func TestManifestDoesNotAdvertiseLocalImportOnNonWindows(t *testing.T) {
	m := newProvider(cnProfile).Manifest()
	if m.Capabilities.LocalImport {
		t.Fatal("LocalImport capability must be false on non-Windows")
	}
	for _, auth := range m.SupportedAuths {
		if auth == provider.AuthLocalImport {
			t.Fatalf("SupportedAuths = %v must not advertise local import", m.SupportedAuths)
		}
	}
}

func TestLocalCryptoUnsupportedOnNonWindows(t *testing.T) {
	if _, err := loadWBKey(); !errors.Is(err, errLocalCryptoUnsupported) {
		t.Fatalf("loadWBKey err = %v", err)
	}
	plain, encrypted, err := decryptWBField(json.RawMessage(`{"$wbEncrypted":1}`), nil)
	if plain != "" || !encrypted || !errors.Is(err, errLocalCryptoUnsupported) {
		t.Fatalf("decryptWBField = (%q, %v, %v)", plain, encrypted, err)
	}
}
