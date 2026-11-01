package gateway

import (
	"strings"
	"testing"
)

func TestDraftCredentialsMapsEndpoints(t *testing.T) {
	cases := []struct {
		name  string
		draft providerDraft
		want  map[string]string
	}{
		{
			name:  "chat 完整端点折成 base_url",
			draft: providerDraft{Key: "sk-abc", Chat: "https://api.deepseek.com/v1/chat/completions"},
			want:  map[string]string{"api_key": "sk-abc", "base_url": "https://api.deepseek.com/v1"},
		},
		{
			name:  "claude OAuth token 走 access_token",
			draft: providerDraft{Key: "sk-ant-oat01-x", Anthropic: "https://api.anthropic.com/v1/messages/"},
			want:  map[string]string{"access_token": "sk-ant-oat01-x", "base_url": "https://api.anthropic.com"},
		},
		{
			name:  "没有 key 时只保留端点（更新账号不该清空旧密钥）",
			draft: providerDraft{Responses: "https://x.example/v1/responses"},
			want:  map[string]string{"base_url": "https://x.example/v1"},
		},
		{
			name:  "空草稿得到空凭证",
			draft: providerDraft{},
			want:  map[string]string{},
		},
	}
	for _, c := range cases {
		got := draftCredentials(&c.draft)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", c.name, k, got[k], v)
			}
		}
	}
}

func TestCustomCoreProviderRoutesByProtocol(t *testing.T) {
	cases := []struct {
		name     string
		draft    providerDraft
		wantID   string
		wantBase string
		wantKey  string
		wantErr  string
	}{
		{
			name:     "chat 落到 openai-compat",
			draft:    providerDraft{Key: "sk-abc", Chat: "https://relay.example/v1/chat/completions"},
			wantID:   "openai-compat",
			wantBase: "https://relay.example/v1",
			wantKey:  "api_key:sk-abc",
		},
		{
			name:     "responses 落到 codex 且用 access_token",
			draft:    providerDraft{Key: "sk-resp", Responses: "https://relay.example/v1/responses"},
			wantID:   "codex",
			wantBase: "https://relay.example/v1",
			wantKey:  "access_token:sk-resp",
		},
		{
			name:     "anthropic 落到 claude-sub 且用 access_token",
			draft:    providerDraft{Key: "sk-ant-api03-x", Anthropic: "https://relay.example/v1/messages"},
			wantID:   "claude-sub",
			wantBase: "https://relay.example",
			wantKey:  "access_token:sk-ant-api03-x",
		},
		{
			name:     "decide 落到 typesafe",
			draft:    providerDraft{Key: "ts-key", Decide: "https://api.typesafe.ai/v1/systemone"},
			wantID:   "typesafe",
			wantBase: "https://api.typesafe.ai",
			wantKey:  "api_key:ts-key",
		},
		{
			name:     "gemini 落到 gemini-compat 并保留 /v1beta",
			draft:    providerDraft{Key: "g-key", Gemini: "https://generativelanguage.googleapis.com/v1beta"},
			wantID:   "gemini-compat",
			wantBase: "https://generativelanguage.googleapis.com/v1beta",
			wantKey:  "api_key:g-key",
		},
		{
			name:    "完全没有端点",
			draft:   providerDraft{Key: "k"},
			wantErr: "Base URL",
		},
		{
			name:    "只有 Gemini 但没填地址",
			draft:   providerDraft{Key: "g-key", Gemini: " "},
			wantErr: "Base URL",
		},
	}
	for _, c := range cases {
		id, creds, problem := customCoreProvider(&c.draft)
		if c.wantErr != "" {
			if id != "" || problem == "" || !strings.Contains(problem, c.wantErr) {
				t.Errorf("%s: id=%q problem=%q, want error containing %q", c.name, id, problem, c.wantErr)
			}
			continue
		}
		if id != c.wantID || problem != "" {
			t.Errorf("%s: id=%q problem=%q, want %q", c.name, id, problem, c.wantID)
			continue
		}
		if creds["base_url"] != c.wantBase {
			t.Errorf("%s: base_url = %q, want %q", c.name, creds["base_url"], c.wantBase)
		}
		gotKey := ""
		for k, v := range creds {
			if strings.HasSuffix(k, "_key") || k == "access_token" {
				gotKey = k + ":" + v
			}
		}
		if gotKey != c.wantKey {
			t.Errorf("%s: key = %q, want %q", c.name, gotKey, c.wantKey)
		}
		if _, ok := creds["api_key"]; ok && (c.wantID == "codex" || c.wantID == "claude-sub") {
			t.Errorf("%s: api_key should be removed for %s", c.name, c.wantID)
		}
	}
}

func TestCleanProviderModels(t *testing.T) {
	got := cleanProviderModels([]string{" a ", "a", "", "b"})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}
