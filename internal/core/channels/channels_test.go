package channels

import (
	"strings"
	"testing"
)

func TestVendorCatalogShape(t *testing.T) {
	kinds := map[string]bool{"vendor": true, "relay": true, "local": true}
	seen := map[string]bool{}
	for _, v := range Vendors() {
		if v.ID == "" || v.DisplayName == "" || v.BaseURL == "" {
			t.Errorf("incomplete vendor: %+v", v)
		}
		if seen[v.ID] {
			t.Errorf("duplicate vendor %q", v.ID)
		}
		seen[v.ID] = true
		if !kinds[v.Kind] {
			t.Errorf("%s: bad kind %q", v.ID, v.Kind)
		}
		if v.Kind == "local" && !v.NoKey {
			t.Errorf("%s: local vendor should not require a key", v.ID)
		}
		for _, m := range v.Models {
			if !strings.HasPrefix(m.ID, v.ID+"/") {
				t.Errorf("%s: model %q lacks prefix", v.ID, m.ID)
			}
			if m.UpstreamModel == "" {
				t.Errorf("%s: model %q has no upstream name", v.ID, m.ID)
			}
		}
		if len(v.Regions) > 0 {
			if v.RegionLabel == "" {
				t.Errorf("%s: regions need a label", v.ID)
			}
			regionIDs := map[string]bool{}
			for _, r := range v.Regions {
				if r.ID == "" || r.Name == "" {
					t.Errorf("%s: incomplete region %+v", v.ID, r)
				}
				if r.Chat == "" && r.Responses == "" && r.Anthropic == "" {
					t.Errorf("%s: region %q has no endpoint", v.ID, r.ID)
				}
				if regionIDs[r.ID] {
					t.Errorf("%s: duplicate region %q", v.ID, r.ID)
				}
				regionIDs[r.ID] = true
			}
		}
	}
	if len(Vendors()) < 10 {
		t.Fatalf("expected a broad catalog, got %d vendors", len(Vendors()))
	}
}

func TestPresetsMirrorVendors(t *testing.T) {
	ps := Presets()
	if len(ps) != len(Vendors()) {
		t.Fatalf("presets = %d, vendors = %d", len(ps), len(Vendors()))
	}
	for i, p := range ps {
		v := Vendors()[i]
		if p.ID != v.ID || p.Chat != v.BaseURL || p.Kind != v.Kind {
			t.Errorf("preset %q does not mirror vendor %+v", p.ID, v)
		}
		if len(p.Regions) != len(v.Regions) || p.RegionLabel != v.RegionLabel {
			t.Errorf("preset %q regions not mirrored", p.ID)
		}
	}
}
