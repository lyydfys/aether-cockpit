package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 纯中文名的自定义渠道必须能建，而且**中文直接成为渠道 id**。
//
// 历史：slugify 只认 ASCII，中文全被滤掉 → 一度直接报"渠道标识不能为空"；
// 后来兜底成 `relay-<名字哈希>`，可以让用户建出来，但模型前缀是
// `custom-relay-3f2a1b/xxx`，完全认不出是哪个渠道。现在中文直接进 slug。
func TestCustomChannelChineseNameBecomesID(t *testing.T) {
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
	if ch.ID != "测试中转" {
		t.Fatalf("中文名应直接成为渠道 id，实际 %q", ch.ID)
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

// slug 规则：ASCII 保留、中文保留、其它折成 `-`、纯符号得空串。
func TestCustomSlugifyRules(t *testing.T) {
	cases := map[string]string{
		"My Relay":          "my-relay",
		"relay_2026":        "relay-2026",
		"公司中转 My Relay":  "公司中转-my-relay",
		"测试中转":             "测试中转",
		"公司 中转":           "公司-中转",
		"中转站（测试）":          "中转站-测试",
	}
	for in, want := range cases {
		if got := slugifyCustomID(in); got != want {
			t.Errorf("slugifyCustomID(%q) = %q，want %q", in, got, want)
		}
	}
	// 纯符号没有可读字符，仍然回退给调用方处理。
	for _, in := range []string{"🎉🎉", "...", "()"} {
		if got := slugifyCustomID(in); got != "" {
			t.Errorf("slugifyCustomID(%q) = %q，want 空串", in, got)
		}
	}
}