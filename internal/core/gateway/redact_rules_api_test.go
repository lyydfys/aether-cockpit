package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aether-cockpit/internal/core/redact"
)

func redactPost(t *testing.T, h http.HandlerFunc, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestRedactRulesSaveApplyRestart(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	g := New(Config{SettingsPath: settings, UsagePath: "-"})
	if code, out := redactPost(t, g.handleRedactSettings, `{"enabled":true}`); code != http.StatusOK {
		t.Fatalf("开启脱敏失败: %d %v", code, out)
	}

	body := `{"rules":[{"kind":"Gateway Key","prefix":"oc_sk_"},{"kind":"badge","regex":"ZW-[A-Z0-9]{6}"}]}`
	code, view := redactPost(t, g.handleRedactRules, body)
	if code != http.StatusOK {
		t.Fatalf("保存规则失败: %d %v", code, view)
	}
	rb, _ := json.Marshal(view["redactRules"])
	var rules []map[string]any
	_ = json.Unmarshal(rb, &rules)
	if len(rules) != 2 {
		t.Fatalf("redactRules = %#v", view["redactRules"])
	}
	first := rules[0]
	if first["kind"] != "GATEWAY_KEY" || first["prefix"] != "oc_sk_" {
		t.Fatalf("第一条规则被规范化错了: %#v", first)
	}

	eng := g.redactEngine()
	res := eng.Mask("use oc_sk_abc123XYZ now", "p", "m")
	if len(res.Hits) != 1 || res.Hits[0].Kind != "GATEWAY_KEY" {
		t.Fatalf("自定义规则未生效: %+v", res.Hits)
	}

	// 落盘：settings.json 里能看到规则（不含任何原值）。
	raw, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "redactRules") || !strings.Contains(string(raw), "oc_sk_") {
		t.Fatalf("设置未落盘: %s", raw)
	}

	// 重启后从设置恢复。
	g2 := New(Config{SettingsPath: settings, UsagePath: "-"})
	got := g2.redactEngine().CustomRules()
	if len(got) != 2 || got[0].Kind != "GATEWAY_KEY" || got[1].Regex != "ZW-[A-Z0-9]{6}" {
		t.Fatalf("重启后规则丢失: %+v", got)
	}
	if !g2.redactEngine().Enabled() {
		t.Fatal("重启后脱敏开关丢失")
	}
}

func TestRedactRulesRejectKeepsOld(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	if code, _ := redactPost(t, g.handleRedactRules, `{"rules":[{"kind":"k","prefix":"oc_"}]}`); code != http.StatusOK {
		t.Fatal("初始规则保存失败")
	}
	code, out := redactPost(t, g.handleRedactRules, `{"rules":[{"kind":"k","regex":"("}]}`)
	if code != http.StatusBadRequest || out["error"] == nil {
		t.Fatalf("坏正则应 400: %d %v", code, out)
	}
	if got := g.redactEngine().CustomRules(); len(got) != 1 || got[0].Prefix != "oc_" {
		t.Fatalf("拒绝后旧规则应保留: %+v", got)
	}
	view := g.settingsView()
	b, _ := json.Marshal(view["redactRules"])
	var rules []map[string]any
	_ = json.Unmarshal(b, &rules)
	if len(rules) != 1 {
		t.Fatalf("设置页应仍显示旧规则: %#v", view["redactRules"])
	}
}

func TestRedactRuleEdit(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	if code, _ := redactPost(t, g.handleRedactRules, `{"rules":[{"kind":"k","prefix":"oc_a_"},{"kind":"k2","prefix":"oc_b_"}]}`); code != http.StatusOK {
		t.Fatal("初始规则保存失败")
	}

	code, view := redactPost(t, g.handleRedactRule,
		`{"at":0,"was":{"kind":"k","prefix":"oc_a_"},"rule":{"kind":"k","prefix":"oc_c_"}}`)
	if code != http.StatusOK {
		t.Fatalf("编辑失败: %d %v", code, view)
	}
	got := g.redactEngine().CustomRules()
	if len(got) != 2 || got[0].Prefix != "oc_c_" || got[1].Prefix != "oc_b_" {
		t.Fatalf("编辑结果不对: %+v", got)
	}

	// 拿旧样子再改（模拟别处已改过）应冲突而不是改错人。
	code, out := redactPost(t, g.handleRedactRule,
		`{"at":0,"was":{"kind":"k","prefix":"oc_a_"},"rule":{"kind":"k","prefix":"oc_d_"}}`)
	if code != http.StatusConflict || out["error"] == nil {
		t.Fatalf("过期编辑应 409: %d %v", code, out)
	}
	if now := g.redactEngine().CustomRules(); now[0].Prefix != "oc_c_" {
		t.Fatalf("冲突后不应改动: %+v", now)
	}
}

func TestRedactAllowSave(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	g := New(Config{SettingsPath: settings, UsagePath: "-"})
	eng := g.redactEngine()
	eng.SetEnabled(true)

	code, view := redactPost(t, g.handleRedactAllow, `{"allow":["ops@example.com"," build-bot "]}`)
	if code != http.StatusOK {
		t.Fatalf("保存白名单失败: %d %v", code, view)
	}
	ab, _ := json.Marshal(view["redactAllow"])
	var allow []string
	_ = json.Unmarshal(ab, &allow)
	if len(allow) != 2 || allow[1] != "build-bot" {
		t.Fatalf("redactAllow = %#v", view["redactAllow"])
	}
	res := eng.Mask("mail OPS@example.com", "p", "m")
	if len(res.Hits) != 0 || res.Text != "mail OPS@example.com" {
		t.Fatalf("白名单未生效: %+v %q", res.Hits, res.Text)
	}

	g2 := New(Config{SettingsPath: settings, UsagePath: "-"})
	if got := g2.redactEngine().Allow(); len(got) != 2 {
		t.Fatalf("重启后白名单丢失: %+v", got)
	}
}

func TestDecodeCustomRulesFromSettingsShape(t *testing.T) {
	// 落盘再读回来时是 []any；确保解得出。
	in := []any{
		map[string]any{"kind": "K", "prefix": "oc_"},
		map[string]any{"kind": "B", "regex": "ZW-1"},
	}
	got := decodeCustomRules(in)
	if len(got) != 2 || got[0] != (redact.CustomRule{Kind: "K", Prefix: "oc_"}) {
		t.Fatalf("decodeCustomRules = %+v", got)
	}
	if allow := decodeAllow([]any{"a", "b"}); len(allow) != 2 {
		t.Fatalf("decodeAllow = %+v", allow)
	}
}

func TestRevealSettings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	g := New(Config{SettingsPath: filepath.Join(dir, "settings.json"), UsagePath: "-"})
	var opened string
	old := openConfigDir
	openConfigDir = func(d string) error { opened = d; return nil }
	t.Cleanup(func() { openConfigDir = old })

	code, out := redactPost(t, g.handleRevealSettings, `{}`)
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("code=%d out=%v", code, out)
	}
	if opened != dir {
		t.Fatalf("打开了 %q，期望 %q", opened, dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("配置目录未创建: %v", err)
	}
	if view := g.settingsView(); view["dir"] != dir {
		t.Fatalf("settingsView.dir = %v", view["dir"])
	}
}

func TestRedactWordsAndKindsSettings(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	g := New(Config{SettingsPath: settings, UsagePath: "-"})

	body := `{"redact":true,"redactPersonal":true,"redactShape":true,
		"redactWords":["Nightjar","acme-corp"],"redactWordsAnyCase":true,
		"redactKinds":{"mac":false,"home":true,"userhost":true,"ip":true}}`
	rec := httptest.NewRecorder()
	g.handleUpdateSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("保存偏好失败: %d %s", rec.Code, rec.Body.String())
	}

	eng := g.redactEngine()
	if got := eng.Words(); len(got) != 2 || got[0] != "Nightjar" {
		t.Fatalf("Words = %v", got)
	}
	if r := eng.Mask("NIGHTJAR and AcMe-Corp", "p", "m"); len(r.Hits) != 2 {
		t.Fatalf("脱敏词（不分大小写）应命中 2 处: %+v", r.Hits)
	}
	// mac 关掉、home 打开。
	if r := eng.Mask("aa:bb:cc:dd:ee:ff", "p", "m"); len(r.Hits) != 0 {
		t.Fatalf("MAC 应被关掉: %+v", r.Hits)
	}
	if r := eng.Mask("/Users/alice/project", "p", "m"); len(r.Hits) != 1 || r.Hits[0].Kind != "HOME" {
		t.Fatalf("HOME 应被打开: %+v", r.Hits)
	}
	if r := eng.Mask("alice@build-01 and 8.8.8.8", "p", "m"); len(r.Hits) != 2 {
		t.Fatalf("USERHOST/IP 应被打开: %+v", r.Hits)
	}
	// 保留密钥格式：发送文本里是同形假值，不是占位符。
	const rawKey = "sk-abcdefghijklmnopqrstuvwxyz123456"
	r := eng.Mask(rawKey, "p", "m")
	if strings.Contains(r.Text, "{{API_KEY_") || !strings.HasPrefix(r.Text, "sk-") || strings.Contains(r.Text, rawKey) {
		t.Fatalf("shape 模式输出不对: %q", r.Text)
	}

	// 重启后从设置恢复。
	g2 := New(Config{SettingsPath: settings, UsagePath: "-"})
	e2 := g2.redactEngine()
	if got := e2.Words(); len(got) != 2 {
		t.Fatalf("重启后脱敏词丢失: %v", got)
	}
	if r := e2.Mask("aa:bb:cc:dd:ee:ff", "p", "m"); len(r.Hits) != 0 {
		t.Fatalf("重启后 MAC 仍应关闭: %+v", r.Hits)
	}
	if r := e2.Mask("/Users/bob/x", "p", "m"); len(r.Hits) != 1 {
		t.Fatalf("重启后 HOME 仍应打开: %+v", r.Hits)
	}
	if !e2.Shape() {
		t.Fatal("重启后保留密钥格式应仍开着")
	}
}
