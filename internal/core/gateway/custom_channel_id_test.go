package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 纯中文名的自定义渠道必须能建：表单上没有"填标识"的位置，
// slugify 会把中文全滤掉，过去直接报"渠道标识不能为空"，用户无路可走。
//
// 兜底约定与 upsertCustomChannelFromDraft 一致：`relay-<名字短哈希>`。
func TestCustomChannelChineseNameGetsFallbackID(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})

	body := `{"name":"测试中转","base_url":"https://relay.example.com/v1","models":["gpt-4o-mini"]}`
	rec := httptest.NewRecorder()
	g.handleCustomChannelSave(rec, httptest.NewRequest(http.MethodPost, "/api/custom-channels", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("中文名应能创建，得到 %d：%s", rec.Code, rec.Body.String())
	}

	g.settingsMu.Lock()
	list := append([]CustomChannel(nil), g.settings.CustomChannels...)
	g.settingsMu.Unlock()
	if len(list) != 1 {
		t.Fatalf("渠道数 = %d，want 1", len(list))
	}
	ch := list[0]
	if ch.Name != "测试中转" {
		t.Fatalf("name = %q", ch.Name)
	}
	if !strings.HasPrefix(ch.ID, "relay-") {
		t.Fatalf("中文名应回退到 relay-<hash>，实际 %q", ch.ID)
	}
	if len(ch.ID) <= len("relay-") {
		t.Fatalf("id 缺少哈希部分：%q", ch.ID)
	}
}

// 同一个中文名重复保存必须是同一条渠道（幂等），不能每次生成新 id。
func TestCustomChannelChineseNameIsIdempotent(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	body := `{"name":"公司中转","base_url":"https://relay.example.com/v1","models":["m1"]}`
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		g.handleCustomChannelSave(rec, httptest.NewRequest(http.MethodPost, "/api/custom-channels", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次保存失败：%d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	g.settingsMu.Lock()
	n := len(g.settings.CustomChannels)
	g.settingsMu.Unlock()
	if n != 1 {
		t.Fatalf("二次保存后渠道数 = %d，want 1（幂等）", n)
	}
}

// 有 ASCII 字符的名字仍走原有 slugify，不要因为加了兜底就改变既有行为。
func TestCustomSlugifyUnchangedForASCII(t *testing.T) {
	cases := map[string]string{
		"My Relay":      "my-relay",
		"relay_2026":    "relay-2026",
		"公司中转 My Relay": "my-relay",
	}
	for in, want := range cases {
		if got := slugifyCustomID(in); got != want {
			t.Errorf("slugifyCustomID(%q) = %q，want %q", in, got, want)
		}
	}
	if got := slugifyCustomID("测试中转"); got != "" {
		t.Fatalf("纯中文仍应返回空串（交给兜底处理），得到 %q", got)
	}
}
