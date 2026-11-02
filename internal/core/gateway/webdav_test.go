package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// davFakeServer 模拟一个最小 WebDAV 服务：
// MKCOL 返回 405（目录已存在），PUT 返回 201 并记录 body，GET 返回预置 body。
type davFakeServer struct {
	mu        sync.Mutex
	mkcols    []string
	putPath   string
	putBody   []byte
	putAuth   string
	putCT     string
	putStatus int    // 非 0 时 PUT 返回该状态码
	getBody   []byte // nil 时 GET 返回 404
}

func (d *davFakeServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		switch r.Method {
		case "MKCOL":
			d.mkcols = append(d.mkcols, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed) // 模拟目录已存在
		case http.MethodPut:
			d.putPath = r.URL.Path
			d.putAuth = r.Header.Get("Authorization")
			d.putCT = r.Header.Get("Content-Type")
			b, _ := io.ReadAll(r.Body)
			d.putBody = b
			if d.putStatus != 0 {
				w.WriteHeader(d.putStatus)
				return
			}
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			if d.getBody == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(d.getBody)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func TestDavPutCreatesDirsAndUploads(t *testing.T) {
	d := &davFakeServer{}
	srv := httptest.NewServer(d.handler())
	defer srv.Close()

	cfg := &WebDAVConfig{URL: srv.URL + "/dav/backup", Username: "user", Password: "pass"}
	data := []byte(`{"hello":"world"}`)
	if err := davPut(cfg, "aether-backup.json", data); err != nil {
		t.Fatalf("davPut: %v", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	// 目录逐级 MKCOL（URL 末尾没写 / 也会规范化后建到 /dav/backup）。
	if len(d.mkcols) != 2 || d.mkcols[0] != "/dav" || d.mkcols[1] != "/dav/backup" {
		t.Fatalf("MKCOL 序列 = %v，期望 [/dav /dav/backup]", d.mkcols)
	}
	if d.putPath != "/dav/backup/aether-backup.json" {
		t.Fatalf("PUT 路径 = %q", d.putPath)
	}
	if string(d.putBody) != string(data) {
		t.Fatalf("PUT body = %q", d.putBody)
	}
	if d.putCT != "application/json" {
		t.Fatalf("Content-Type = %q", d.putCT)
	}
	if d.putAuth == "" {
		t.Fatal("缺少 Basic Auth 头")
	}
}

func TestDavPutServerErrorIncludesStatus(t *testing.T) {
	d := &davFakeServer{}
	srv := httptest.NewServer(d.handler())
	defer srv.Close()

	cfg := &WebDAVConfig{URL: srv.URL + "/dav/"}
	d.putStatus = http.StatusInternalServerError
	err := davPut(cfg, "aether-backup.json", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("期望带状态码的错误，得到 %v", err)
	}
}

func TestDavGetSuccessAnd404(t *testing.T) {
	d := &davFakeServer{}
	srv := httptest.NewServer(d.handler())
	defer srv.Close()

	cfg := &WebDAVConfig{URL: srv.URL + "/dav/backup/", Username: "user", Password: "pass"}

	// 404 → 明确文案。
	_, err := davGet(cfg, "aether-backup.json")
	if err == nil || !strings.Contains(err.Error(), "远端还没有备份文件") {
		t.Fatalf("期望 404 明确文案，得到 %v", err)
	}

	// 成功。
	d.getBody = []byte(`{"version":1,"exported_at":1700000000}`)
	b, err := davGet(cfg, "aether-backup.json")
	if err != nil {
		t.Fatalf("davGet: %v", err)
	}
	if string(b) != string(d.getBody) {
		t.Fatalf("davGet body = %q", b)
	}
}

func TestWebDAVPushPullRoundTrip(t *testing.T) {
	d := &davFakeServer{}
	srv := httptest.NewServer(d.handler())
	defer srv.Close()

	g := New(Config{
		Addr:         "127.0.0.1:7860",
		SettingsPath: filepath.Join(t.TempDir(), "settings.json"),
	})
	enc, err := encryptPassphrase("roundtrip pass")
	if err != nil {
		t.Fatal(err)
	}
	g.settingsMu.Lock()
	g.settings.Sync = &SyncConfig{
		Kind: "webdav", PassphraseEnc: enc,
		WebDAV: &WebDAVConfig{URL: srv.URL, Username: "user", Password: "pass"},
	}
	g.settingsMu.Unlock()

	// --- push ---
	req := httptest.NewRequest(http.MethodPost, "/api/backup/push", nil)
	rec := httptest.NewRecorder()
	g.handleBackupPush(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("push 状态 = %d body=%s", rec.Code, rec.Body.String())
	}
	var pushRes struct {
		Pushed bool   `json:"pushed"`
		Bytes  int    `json:"bytes"`
		Remote string `json:"remote"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pushRes); err != nil {
		t.Fatal(err)
	}
	if !pushRes.Pushed || pushRes.Bytes == 0 || pushRes.Remote == "" {
		t.Fatalf("push 响应异常: %+v", pushRes)
	}

	// 上传的备份不应包含 WebDAV 配置（明文密码不上云）。
	plain, err := openBackup(d.putBody, "roundtrip pass")
	if err != nil {
		t.Fatalf("远端备份应能用口令解开: %v", err)
	}
	var pushedBundle backupBundle
	if err := json.Unmarshal(plain, &pushedBundle); err != nil {
		t.Fatal(err)
	}
	if pushedBundle.Settings == nil || pushedBundle.Settings.WebDAV != nil {
		t.Fatal("备份 bundle 不应包含 WebDAV 配置")
	}
	if pushRes.Bytes != len(d.putBody) {
		t.Fatalf("bytes = %d，实际上传 %d", pushRes.Bytes, len(d.putBody))
	}

	// --- pull：远端返回刚上传的内容 ---
	d.getBody = d.putBody
	req = httptest.NewRequest(http.MethodPost, "/api/backup/pull", nil)
	rec = httptest.NewRecorder()
	g.handleBackupPull(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pull 状态 = %d body=%s", rec.Code, rec.Body.String())
	}
	var pullRes struct {
		Imported map[string]bool `json:"imported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pullRes); err != nil {
		t.Fatal(err)
	}
	if len(pullRes.Imported) == 0 || !pullRes.Imported["settings"] {
		t.Fatalf("imported = %v", pullRes.Imported)
	}
}

func TestBackupPushPullWithoutWebDAVConfig(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json")})

	for _, name := range []string{"/api/backup/push", "/api/backup/pull"} {
		req := httptest.NewRequest(http.MethodPost, name, nil)
		rec := httptest.NewRecorder()
		if name == "/api/backup/push" {
			g.handleBackupPush(rec, req)
		} else {
			g.handleBackupPull(rec, req)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 状态 = %d，期望 400", name, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "尚未配置云同步") && !strings.Contains(rec.Body.String(), "尚未配置 WebDAV") {
			t.Fatalf("%s 响应缺少提示: %s", name, rec.Body.String())
		}
	}
}

func TestSettingsWebDAVPasswordMasking(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json")})

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		g.handleUpdateSettings(rec, req)
		return rec
	}

	// 写入真实密码 → 响应打码。
	rec := post(`{"webdav":{"url":"https://dav.example.com/dav/backup/","username":"me","password":"s3cret"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态 = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("更新响应泄露明文密码: %s", rec.Body.String())
	}

	// GET 也打码。
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec = httptest.NewRecorder()
	g.handleSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET 状态 = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "s3cret") {
		t.Fatalf("GET 响应泄露明文密码: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), webdavPasswordMask) {
		t.Fatalf("GET 响应缺少打码占位符: %s", rec.Body.String())
	}

	// 回写打码值不覆盖旧密码（落盘文件里仍是明文）。
	rec = post(`{"webdav":{"url":"https://dav.example.com/dav/backup/","username":"me","password":"******"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("回写状态 = %d body=%s", rec.Code, rec.Body.String())
	}
	b, err := os.ReadFile(g.settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "s3cret") {
		t.Fatalf("回写打码值后旧密码丢失: %s", b)
	}
	if strings.Contains(string(b), webdavPasswordMask) {
		t.Fatalf("打码占位符不应落盘: %s", b)
	}
}

func TestStartWebDAVAutoPushDisabled(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json")})

	// interval<=0 与未配置：都返回空 stop 函数，调用不应 panic。
	g.StartWebDAVAutoPush(context.Background(), 0)()
	g.StartWebDAVAutoPush(context.Background(), -time.Second)()
}

func TestStartWebDAVAutoPushRuns(t *testing.T) {
	d := &davFakeServer{}
	srv := httptest.NewServer(d.handler())
	defer srv.Close()

	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	enc, err := encryptPassphrase("auto push pass")
	if err != nil {
		t.Fatal(err)
	}
	g.settingsMu.Lock()
	g.settings.Sync = &SyncConfig{
		Kind: "webdav", PassphraseEnc: enc,
		WebDAV: &WebDAVConfig{URL: srv.URL},
	}
	g.settingsMu.Unlock()

	stop := g.StartWebDAVAutoPush(context.Background(), 10*time.Millisecond)
	defer stop()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		got := d.putBody != nil
		d.mu.Unlock()
		if got {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("自动推送未在 3 秒内执行")
}
