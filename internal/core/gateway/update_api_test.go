package gateway

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newUpdateGateway(t *testing.T) http.Handler {
	t.Helper()
	g := New(Config{
		MaxAttempts:      1,
		FirstByteTimeout: time.Second,
		StateTTL:         time.Minute,
		MaxRecords:       10,
		SettingsPath:     t.TempDir() + "/settings.json",
	})
	return g
}

func getUpdate(t *testing.T, h http.Handler, url string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/update", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是 JSON: %v %s", err, rec.Body.String())
	}
	return out
}

func TestUpdateCheck(t *testing.T) {
	oldVer := BuildVersion
	BuildVersion = "0.1.0"
	t.Cleanup(func() { BuildVersion = oldVer })

	h := newUpdateGateway(t)

	// 无清单更新源时回落到 GitHub；用本地 404 假 API，避免测试访问真实 GitHub。
	noRelease := httptest.NewServer(http.NotFoundHandler())
	defer noRelease.Close()
	t.Setenv("AETHER_UPDATE_URL", "")
	t.Setenv("AETHER_GITHUB_API", noRelease.URL)
	t.Setenv("AETHER_GITHUB_REPO", "test/none")
	out := getUpdate(t, h, "")
	if out["available"] != false || out["reason"] == nil {
		t.Fatalf("无更新源/无 Release 应如实说明: %v", out)
	}

	newer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"0.2.0","url":"https://example.com/a.zip","notes":"n"}`))
	}))
	defer newer.Close()
	t.Setenv("AETHER_UPDATE_URL", newer.URL)
	out = getUpdate(t, h, newer.URL)
	if out["available"] != true || out["latest"] != "0.2.0" || out["url"] == "" {
		t.Fatalf("应发现新版本: %v", out)
	}

	same := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"0.1.0"}`))
	}))
	defer same.Close()
	t.Setenv("AETHER_UPDATE_URL", same.URL)
	out = getUpdate(t, h, same.URL)
	if out["available"] != false || out["latest"] != "0.1.0" {
		t.Fatalf("同版本不应提示更新: %v", out)
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer broken.Close()
	t.Setenv("AETHER_UPDATE_URL", broken.URL)
	out = getUpdate(t, h, broken.URL)
	if out["available"] != false || out["reason"] == nil {
		t.Fatalf("坏清单应说明原因: %v", out)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.1.0", 1},
		{"0.1.0", "0.2.0", -1},
		{"v0.1.0", "0.1.0", 0},
		{"0.10.0", "0.9.9", 1},
		{"1.0", "1.0.0", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Fatalf("compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestUpdateDownloadVerifiesSHA(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AETHER_DOWNLOAD_DIR", dir)
	payload := []byte("fake-zip-bytes-for-test")
	sum := sha256.Sum256(payload)
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer fileSrv.Close()

	g := newUpdateGateway(t)
	post := func(body string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(body))
		rec := httptest.NewRecorder()
		g.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	body, _ := json.Marshal(map[string]any{"url": fileSrv.URL + "/aether.zip", "sha256": fmt.Sprintf("%x", sum)})
	code, out := post(string(body))
	if code != 200 || out["verified"] != true {
		t.Fatalf("下载应成功: %d %v", code, out)
	}
	saved, _ := out["path"].(string)
	if !strings.HasPrefix(saved, dir) {
		t.Fatalf("保存路径越界: %q", saved)
	}
	got, err := os.ReadFile(saved)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("文件内容不符: %v", err)
	}
	_ = os.Remove(saved)

	// 校验失败：不落最终文件
	body, _ = json.Marshal(map[string]any{"url": fileSrv.URL + "/aether.zip", "sha256": "deadbeef"})
	code, out = post(string(body))
	if code != 400 {
		t.Fatalf("SHA 不符应 400: %d %v", code, out)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".zip") {
			t.Fatalf("校验失败不应留下 zip: %v", entries)
		}
	}

	// 路径穿越文件名被收敛到下载目录内
	body, _ = json.Marshal(map[string]any{"url": fileSrv.URL + "/x.zip", "name": "../../evil.zip"})
	code, out = post(string(body))
	if code != 200 {
		t.Fatalf("下载应成功: %d %v", code, out)
	}
	saved, _ = out["path"].(string)
	if filepath.Dir(saved) != dir || filepath.Base(saved) != "evil.zip" {
		t.Fatalf("文件名未收敛: %q", saved)
	}
	_ = os.Remove(saved)

	// 非 http(s) 地址拒绝
	body, _ = json.Marshal(map[string]any{"url": "file:///etc/passwd"})
	if code, _ := post(string(body)); code != 400 {
		t.Fatalf("非 http(s) 应 400: %d", code)
	}
}

func TestUpdateCheckGitHub(t *testing.T) {
	oldVer := BuildVersion
	BuildVersion = "0.1.0"
	t.Cleanup(func() { BuildVersion = oldVer })

	var gotAuth, gotAccept string
	digest := strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		if r.URL.Path != "/repos/acme/app/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{
			"tag_name":"v0.2.0","name":"v0.2.0","body":"## 新增\n- GitHub 更新","html_url":"https://github.com/acme/app/releases/tag/v0.2.0",
			"published_at":"2026-11-01T00:00:00Z",
			"assets":[
				{"id":7,"name":"aether-cockpit-0.2.0-windows-amd64.zip","size":123,"digest":"sha256:%s"},
				{"id":8,"name":"checksums.txt","size":10}
			]
		}`, digest)
	}))
	defer srv.Close()

	t.Setenv("AETHER_UPDATE_URL", "")
	t.Setenv("AETHER_GITHUB_API", srv.URL)
	t.Setenv("AETHER_GITHUB_REPO", "acme/app")
	t.Setenv("AETHER_GITHUB_TOKEN", "tok-123")

	h := newUpdateGateway(t)
	out := getUpdate(t, h, srv.URL)
	if out["available"] != true || out["latest"] != "0.2.0" || out["source"] != "github" {
		t.Fatalf("GitHub 应发现新版本: %v", out)
	}
	if out["github"] != true || !strings.HasSuffix(fmt.Sprint(out["url"]), "/releases/assets/7") {
		t.Fatalf("应选择 windows zip 资产: %v", out)
	}
	if out["sha256"] != digest || out["downloadable"] != true {
		t.Fatalf("资产校验信息缺失: %v", out)
	}
	if !strings.Contains(fmt.Sprint(out["notes"]), "GitHub 更新") {
		t.Fatalf("发布说明缺失: %v", out)
	}
	if gotAuth != "Bearer tok-123" || gotAccept == "" {
		t.Fatalf("GitHub 请求头 auth=%q accept=%q", gotAuth, gotAccept)
	}

	// 仓库没有 Release：latest 与列表都 404 → 明确提示。
	empty := httptest.NewServer(http.NotFoundHandler())
	defer empty.Close()
	t.Setenv("AETHER_GITHUB_API", empty.URL)
	t.Setenv("AETHER_GITHUB_REPO", "acme/empty")
	out = getUpdate(t, h, empty.URL)
	if out["available"] != false || out["reason"] == nil ||
		!strings.Contains(fmt.Sprint(out["reason"]), "Release") {
		t.Fatalf("无 Release 应说明原因: %v", out)
	}
}

func TestGitHubAssetDownloadSendsOctetStream(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AETHER_DOWNLOAD_DIR", dir)
	t.Setenv("AETHER_GITHUB_TOKEN", "tok-secret")
	payload := []byte("github-asset-bytes")
	sum := sha256.Sum256(payload)
	var gotAccept, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept, gotAuth = r.Header.Get("Accept"), r.Header.Get("Authorization")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	t.Setenv("AETHER_GITHUB_API", srv.URL)
	g := newUpdateGateway(t)
	body, _ := json.Marshal(map[string]any{
		"url": srv.URL + "/repos/acme/app/releases/assets/7", "github": true,
		"name": "aether-cockpit-0.2.0-windows-amd64.zip", "sha256": fmt.Sprintf("%x", sum),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out["verified"] != true {
		t.Fatalf("GitHub 资产下载失败: %d %v", rec.Code, out)
	}
	if gotAccept != "application/octet-stream" || gotAuth != "Bearer tok-secret" {
		t.Fatalf("资产请求头 accept=%q auth=%q", gotAccept, gotAuth)
	}
	_ = os.Remove(fmt.Sprint(out["path"]))
}

func TestUpdateSourceSettingsMasked(t *testing.T) {
	h := newUpdateGateway(t)
	post := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	get := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	if code := post(`{"update_repo":"acme/app","update_token":"ghp-secret"}`); code != 200 {
		t.Fatalf("保存更新源失败: %d", code)
	}
	view := get()
	if view["update_repo"] != "acme/app" || view["update_token"] != "******" {
		t.Fatalf("更新源回显 = %v / %v", view["update_repo"], view["update_token"])
	}

	// 打码值回传不能覆盖真令牌。
	if code := post(`{"update_token":"******"}`); code != 200 {
		t.Fatalf("回传打码值失败: %d", code)
	}
	g := h.(*Gateway)
	g.settingsMu.Lock()
	token := g.settings.UpdateToken
	g.settingsMu.Unlock()
	if token != "ghp-secret" {
		t.Fatalf("令牌被覆盖: %q", token)
	}

	if code := post(`{"update_repo":"not a repo"}`); code != 400 {
		t.Fatalf("非法仓库应 400: %d", code)
	}
	if code := post(`{"update_token":"-"}`); code != 200 {
		t.Fatalf("清除令牌失败: %d", code)
	}
	g.settingsMu.Lock()
	token = g.settings.UpdateToken
	g.settingsMu.Unlock()
	if token != "" {
		t.Fatalf("令牌未清除: %q", token)
	}
}

func TestGitHubAssetDownloadRejectsForeignHost(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AETHER_DOWNLOAD_DIR", dir)
	t.Setenv("AETHER_GITHUB_TOKEN", "tok-secret")
	t.Setenv("AETHER_GITHUB_API", "https://api.github.com")
	leaked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked = true
		}
		_, _ = w.Write([]byte("should-not-download"))
	}))
	defer srv.Close()

	g := newUpdateGateway(t)
	body, _ := json.Marshal(map[string]any{"url": srv.URL + "/evil", "github": true, "name": "x.zip"})
	req := httptest.NewRequest(http.MethodPost, "/api/update/download", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	g.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("异源 GitHub 下载应 400，实际 %d", rec.Code)
	}
	if leaked {
		t.Fatal("令牌被发送到了非配置主机")
	}
}
