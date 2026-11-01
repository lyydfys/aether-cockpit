package provider

import "testing"

func TestAccountModelMapping(t *testing.T) {
	env := NewAccountEnv("a1", "openai-compat")
	env.SetModelMapping(map[string]string{
		"gpt-4o":             "up-exact",
		"claude-*":           "up-claude",
		"claude-sonnet-*":    "up-sonnet",
		"*-latest":           "up-latest",
		"a-*-b":              "up-mid",
		"":                   "ignored",
		"same":               "same",
	})
	cases := map[string]string{
		"gpt-4o":             "up-exact",
		"claude-opus-4":      "up-claude",
		"claude-sonnet-4-6":  "up-sonnet", // 最长通配优先
		"qwen-latest":        "up-latest",
		"a-x-b":              "up-mid",
		"unmatched-model":    "",
		"same":               "",
	}
	for in, want := range cases {
		if got := env.MapModel(in); got != want {
			t.Fatalf("MapModel(%q) = %q, want %q", in, got, want)
		}
	}
	if env.MapModel("") != "" {
		t.Fatal("空模型名不应映射")
	}

	// 快照必须带映射副本，且修改快照不影响账号。
	snap := env.Snapshot()
	if snap.ModelMapping["gpt-4o"] != "up-exact" {
		t.Fatalf("snapshot = %+v", snap.ModelMapping)
	}
	snap.ModelMapping["gpt-4o"] = "mutated"
	if env.MapModel("gpt-4o") != "up-exact" {
		t.Fatal("快照与账号共享了 map")
	}

	// 空映射清除。
	env.SetModelMapping(nil)
	if env.MapModel("gpt-4o") != "" || len(env.ModelMapping()) != 0 {
		t.Fatal("空映射应清除")
	}
}

func TestMatchModelGlob(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"claude-*", "claude-opus", true},
		{"claude-*", "gpt-4o", false},
		{"*-latest", "qwen-latest", true},
		{"*-latest", "latest-x", false},
		{"a-*-b", "a-x-b", true},
		{"a-*-b", "a-x-y-b", true},
		{"a-*-b", "a--b", true},
		{"a-*-b", "a-x", false},
		{"*", "anything", true},
		{"no-star", "no-star", true},
		{"no-star", "other", false},
	}
	for _, c := range cases {
		if got := matchModelGlob(c.pat, c.name); got != c.want {
			t.Fatalf("matchModelGlob(%q,%q) = %v, want %v", c.pat, c.name, got, c.want)
		}
	}
}
