package gateway

import (
	"testing"

	"aether-cockpit/internal/provider"
	_ "aether-cockpit/internal/providers/antigravity"
	_ "aether-cockpit/internal/providers/antigravityide"
)

func TestAliasModelsRouteBackToProvider(t *testing.T) {
	r := NewModelRouter()
	r.Rebuild()
	for _, m := range provider.AllManifests() {
		for _, mm := range m.Models {
			id := mm.ID
			found := false
			for _, b := range r.Resolve(id) {
				if b.ProviderID == m.ID {
					found = true
				}
			}
			if !found {
				t.Errorf("%s/%s 无法路由回 %s", m.ID, id, m.ID)
			}
		}
	}
}
