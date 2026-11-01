package reverse

import "testing"

func findProfile(t *testing.T, id string) Profile {
	t.Helper()
	for _, p := range profiles() {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("没有 %s profile", id)
	return Profile{}
}

func TestCodexOfficialFirst(t *testing.T) {
	codex := New(findProfile(t, "codex"))
	if !codex.Manifest().Capabilities.OfficialFirst {
		t.Fatal("codex 应声明 official_first")
	}
	cases := []struct {
		creds map[string]string
		want  bool
	}{
		{map[string]string{"account_id": "acc"}, true},
		{map[string]string{"refresh_token": "rt"}, true},
		{map[string]string{"base_url": "https://relay.example", "access_token": "tok"}, false},
		{map[string]string{}, false},
	}
	for _, c := range cases {
		if got := codex.IsOfficialAccount(c.creds); got != c.want {
			t.Errorf("IsOfficialAccount(%v) = %v, want %v", c.creds, got, c.want)
		}
	}

	if New(findProfile(t, "opencode")).Manifest().Capabilities.OfficialFirst {
		t.Fatal("opencode 不应声明 official_first")
	}
}
