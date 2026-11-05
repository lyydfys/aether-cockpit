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
}

// OpenCode 渠道是"官方 Zen + 自部署/中转"混装的：同一个渠道里两种端点都可能
// 存在。目标是在都有额度时先走官方，官方不可用时再落到自部署上——既保官方
// 的计费/能力口径，又不会因为官方限流把渠道打死。
func TestOpencodePrefersOfficialEndpoint(t *testing.T) {
	for _, id := range []string{"opencode", "opencode-go"} {
		p := New(findProfile(t, id))
		if !p.Manifest().Capabilities.OfficialFirst {
			t.Fatalf("%s 应声明 official_first", id)
		}
		cases := []struct {
			name  string
			creds map[string]string
			want  bool
		}{
			{"无 base_url = 走默认官方端点", map[string]string{"api_key": "k"}, true},
			{"官方根域", map[string]string{"base_url": "https://opencode.ai/zen/v1"}, true},
			{"官方子域", map[string]string{"base_url": "https://zen.opencode.ai/v1"}, true},
			{"官方子域带端口与大写", map[string]string{"base_url": "HTTPS://API.OpenCode.AI:443/v1"}, true},
			{"自部署", map[string]string{"base_url": "https://relay.example.com/v1"}, false},
			{"自部署内网", map[string]string{"base_url": "http://192.168.1.10:8080/v1"}, false},
			// 域名边界：只有后缀才算官方，不能被子串骗过去。
			{"后缀伪装", map[string]string{"base_url": "https://opencode.ai.evil.com/v1"}, false},
			{"前缀伪装", map[string]string{"base_url": "https://notopencode.ai/v1"}, false},
			{"写坏的地址", map[string]string{"base_url": "::://bad"}, false},
		}
		for _, c := range cases {
			if got := p.IsOfficialAccount(c.creds); got != c.want {
				t.Errorf("%s / %s: IsOfficialAccount(%v) = %v, want %v",
					id, c.name, c.creds, got, c.want)
			}
		}
	}
}

// 官方判定只影响排序，不改变账号可用性：自部署账号照样能用。
func TestOpencodeOfficialIsPurePredicate(t *testing.T) {
	relay := map[string]string{"base_url": "https://relay.example.com/v1", "api_key": "k"}
	if opencodeIsOfficial(relay) {
		t.Fatal("自部署端点不该判为官方")
	}
	if relay["api_key"] != "k" {
		t.Fatal("判定过程不该修改凭证")
	}
}
