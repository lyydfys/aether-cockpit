package typesafe

import "testing"

func TestManifest(t *testing.T) {
	p := &Provider{}
	m := p.Manifest()
	if m.ID != "typesafe" || m.Family != "typesafe" {
		t.Fatalf("manifest id/family = %q/%q", m.ID, m.Family)
	}
	if m.Capabilities.Chat || m.Capabilities.Quota {
		t.Errorf("typesafe must not claim chat/quota capabilities: %+v", m.Capabilities)
	}
	if len(m.Models) != 1 || m.Models[0].UpstreamModel != "jev" {
		t.Errorf("models = %+v", m.Models)
	}
	if m.Models[0].Capabilities.Streaming {
		t.Errorf("jev must not claim streaming")
	}
	if len(m.AuthFormSchema) != 2 || m.AuthFormSchema[1].Key != "api_key" || !m.AuthFormSchema[1].Required {
		t.Errorf("auth form = %+v", m.AuthFormSchema)
	}
}
