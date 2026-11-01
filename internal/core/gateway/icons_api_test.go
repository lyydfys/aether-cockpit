package gateway

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

var tinyPNG = append([]byte("\x89PNG\r\n\x1a\n"), []byte("test-body")...)

func iconPost(t *testing.T, g *Gateway, h http.HandlerFunc, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestIconUploadServe(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})

	body := `{"data":"` + base64.StdEncoding.EncodeToString(tinyPNG) + `"}`
	code, out := iconPost(t, g, g.handleIcons, body)
	if code != http.StatusOK {
		t.Fatalf("upload = %d %v", code, out)
	}
	icon, _ := out["icon"].(string)
	if !strings.HasPrefix(icon, "file:") || !strings.HasSuffix(icon, ".png") {
		t.Fatalf("icon = %q", icon)
	}
	name := strings.TrimPrefix(icon, "file:")

	// 同名再次上传：内容寻址，名字一致。
	if _, out2 := iconPost(t, g, g.handleIcons, body); out2["icon"] != icon {
		t.Fatalf("同内容应同名: %v", out2["icon"])
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/icons/"+name, nil)
	req.SetPathValue("name", name)
	g.handleIconGet(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("serve = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.HasPrefix(rec.Body.String(), "\x89PNG") {
		t.Fatal("返回的不是原图")
	}

	// 非法名字（路径穿越、未知文件）一律 404。
	for _, bad := range []string{"../settings.json", "zzz.png", name + "/x"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/api/icons/x", nil)
		req.SetPathValue("name", bad)
		g.handleIconGet(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%q 应 404，得到 %d", bad, rec.Code)
		}
	}

	// 非图片数据拒绝。
	if code, _ := iconPost(t, g, g.handleIcons, `{"data":"`+base64.StdEncoding.EncodeToString([]byte("not an image"))+`"}`); code != http.StatusBadRequest {
		t.Fatalf("非图片应 400，得到 %d", code)
	}
}

func TestIconFaviconFetch(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/favicon.ico" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = w.Write(tinyPNG)
	}))
	defer site.Close()

	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	code, out := iconPost(t, g, g.handleIconFavicon, `{"url":"`+site.URL+`/v1/chat/completions"}`)
	if code != http.StatusOK {
		t.Fatalf("favicon = %d %v", code, out)
	}
	if icon, _ := out["icon"].(string); !strings.HasPrefix(icon, "file:") {
		t.Fatalf("icon = %v", out["icon"])
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer bad.Close()
	if code, out := iconPost(t, g, g.handleIconFavicon, `{"url":"`+bad.URL+`"}`); code != http.StatusBadGateway {
		t.Fatalf("无图标应 502，得到 %d %v", code, out)
	}
}

func TestProviderIconPersistsAndShows(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	g := New(Config{SettingsPath: settings, UsagePath: "-"})
	g.settingsMu.Lock()
	g.setProviderIconLocked("mock-native", "file:abc12345.png")
	_ = g.saveSettings()
	g.settingsMu.Unlock()

	rec := httptest.NewRecorder()
	g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	var envelope struct {
		Providers []struct {
			ID   string `json:"id"`
			Icon string `json:"icon"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range envelope.Providers {
		if p.ID == "mock-native" {
			found = true
			if p.Icon != "file:abc12345.png" {
				t.Fatalf("icon = %q", p.Icon)
			}
		}
	}
	if !found {
		t.Fatal("providers 未列出 mock-native")
	}

	g2 := New(Config{SettingsPath: settings, UsagePath: "-"})
	if got := g2.providerIcon("mock-native"); got != "file:abc12345.png" {
		t.Fatalf("重启后 icon = %q", got)
	}
}

func TestIconBrandFallback(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/icons/mock-native.svg", nil)
	req.SetPathValue("file", "mock-native-color.svg")
	g.handleIcon(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "svg") {
		t.Fatalf("brand icon = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "<svg") {
		t.Fatal("不是 SVG")
	}
	// 未知名字也能出贴片（客户端/自定义名字）：按哈希取色 + 首字母。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/icons/claude_code.svg", nil)
	req.SetPathValue("file", "claude_code.svg")
	g.handleIcon(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), ">C</text>") {
		t.Fatalf("未知名字贴片 = %d %s", rec.Code, rec.Body.String())
	}
	// 路径穿越仍拒绝。
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/icons/..%5Csettings.json", nil)
	req.SetPathValue("file", `..\settings.json`)
	g.handleIcon(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("路径穿越应 404，得到 %d", rec.Code)
	}
}

func TestIconFaviconFromPageLink(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<html><head><link rel="shortcut icon" href="/assets/icon.png"></head><body>hi</body></html>`)
		case "/assets/icon.png":
			_, _ = w.Write(tinyPNG)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()

	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	// 给定地址不是图片、根 favicon.ico/png 也不存在 → 必须走首页 <link rel=icon>。
	code, out := iconPost(t, g, g.handleIconFavicon, `{"url":"`+site.URL+`/v1/chat/completions"}`)
	if code != http.StatusOK {
		t.Fatalf("favicon from page = %d %v", code, out)
	}
	if icon, _ := out["icon"].(string); !strings.HasPrefix(icon, "file:") {
		t.Fatalf("icon = %v", out["icon"])
	}
}

func TestBackupCarriesProviderIcons(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	icon, err := g.saveIcon(append(tinyPNG, []byte("roundtrip")...))
	if err != nil {
		t.Fatal(err)
	}
	g.settingsMu.Lock()
	g.setProviderIconLocked("mock-native", icon)
	_ = g.saveSettings()
	g.settingsMu.Unlock()

	bundle := g.buildBackupBundle()
	if len(bundle.Icons) == 0 {
		t.Fatal("备份应带上图标")
	}
	name := strings.TrimPrefix(icon, "file:")
	if _, ok := bundle.Icons[name]; !ok {
		t.Fatalf("备份缺少 %s: %v", name, bundle.Icons)
	}

	// 新机器（新目录）恢复：图标文件写回，引用可解析。
	g2 := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	applied := g2.applyBundle(&bundle)
	if !applied["icons"] {
		t.Fatalf("恢复后应标记 icons: %v", applied)
	}
	if got := g2.providerIcon("mock-native"); got != icon {
		t.Fatalf("恢复后引用 = %q", got)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/icons/"+name, nil)
	req.SetPathValue("name", name)
	g2.handleIconGet(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("恢复后读取图标 = %d", rec.Code)
	}
}

func TestImportIconDirectURL(t *testing.T) {
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/logo.png" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(tinyPNG)
	}))
	defer img.Close()

	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	code, out := iconPost(t, g, g.handleImportIcon, `{"url":"`+img.URL+`/logo.png"}`)
	if code != http.StatusOK {
		t.Fatalf("import icon = %d %v", code, out)
	}
	if icon, _ := out["icon"].(string); !strings.HasPrefix(icon, "file:") {
		t.Fatalf("icon = %v", out["icon"])
	}

	// 非图片地址 → 400，不上车。
	txt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "<html>not an image</html>")
	}))
	defer txt.Close()
	if code, _ := iconPost(t, g, g.handleImportIcon, `{"url":"`+txt.URL+`"}`); code != http.StatusBadRequest {
		t.Fatalf("非图片应 400，得到 %d", code)
	}
}

func TestProvidersAndAgentsDefaultIcons(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	rec := httptest.NewRecorder()
	g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	var envelope struct {
		Providers []struct {
			ID   string `json:"id"`
			Icon string `json:"icon"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range envelope.Providers {
		if p.ID == "mock-native" {
			found = true
			if p.Icon != "mock-native-color" {
				t.Fatalf("默认 icon 应为渠道 id，得到 %q", p.Icon)
			}
		}
	}
	if !found {
		t.Fatal("providers 未列出 mock-native")
	}

	// Agents 行带 id 作为图标名，页面才能画出贴片。
	for _, a := range g.buildMagpieState().Agents {
		if a.Icon != a.ID+"-color" {
			t.Fatalf("agent %s icon=%q", a.ID, a.Icon)
		}
	}
}
