package antigravityide

import (
	"strings"
	"testing"

	"aether-cockpit/internal/provider"
)

func TestManifestAlias(t *testing.T) {
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_ID", "test-client-id")
	t.Setenv("AETHER_ANTIGRAVITY_OAUTH_CLIENT_SECRET", "test-client-secret")
	m := (&Provider{}).Manifest()
	if m.ID != "antigravity-ide" || m.DisplayName != "Antigravity IDE" || m.Family != "antigravity" {
		t.Fatalf("manifest 标识不对: %+v", m)
	}
	if m.OAuth == nil || len(m.SupportedAuths) == 0 {
		t.Fatal("缺少 OAuth/登录方式")
	}
	if len(m.Models) == 0 {
		t.Fatal("没有模型")
	}
	for _, model := range m.Models {
		if !strings.HasPrefix(model.ID, "antigravity-ide/") {
			t.Fatalf("模型前缀未替换: %s", model.ID)
		}
	}
}

func TestCapabilities(t *testing.T) {
	var p provider.BaseProvider = &Provider{}
	if _, ok := p.(provider.ChatCapability); !ok {
		t.Fatal("缺少 ChatCapability")
	}
	if _, ok := p.(provider.LocalImportCapability); !ok {
		t.Fatal("缺少 LocalImportCapability")
	}
	if _, ok := p.(provider.QuotaCapability); !ok {
		t.Fatal("缺少 QuotaCapability")
	}
}
