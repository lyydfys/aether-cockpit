package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSealBackupRoundTrip(t *testing.T) {
	plain := []byte(`{"hello":"world"}`)
	sealed, err := sealBackup(plain, "correct horse")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if !sealedBackup(sealed) || bytes.Contains(sealed, []byte("hello")) {
		t.Fatal("密文里不应出现明文")
	}
	got, err := openBackup(sealed, "correct horse")
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := openBackup(sealed, "wrong"); err == nil {
		t.Fatal("错误口令应失败")
	}
	if _, err := openBackup(sealed, ""); err == nil {
		t.Fatal("缺口令应失败")
	}
	// 旧版明文备份原样通过
	legacy, err := openBackup(plain, "")
	if err != nil || !bytes.Equal(legacy, plain) {
		t.Fatalf("明文应原样返回: %v", err)
	}
}

// AWS 官方 SigV4 示例（GET Object，含 Range 头）。
// https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-header-based-auth.html
func TestSignV4AgainstAWSVector(t *testing.T) {
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	headers := map[string]string{
		"host":                 "examplebucket.s3.amazonaws.com",
		"range":                "bytes=0-9",
		"x-amz-content-sha256": s3EmptyPayloadHash,
		"x-amz-date":           "20130524T000000Z",
	}
	auth := signV4(http.MethodGet, "/test.txt", "", headers, s3EmptyPayloadHash,
		"us-east-1", "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", now)
	want := "Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if !strings.Contains(auth, want) {
		t.Fatalf("SigV4 与 AWS 示例不一致:\n%s", auth)
	}
}

func TestS3PutGetRoundTrip(t *testing.T) {
	var stored []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") {
			w.WriteHeader(403)
			return
		}
		if r.Header.Get("x-amz-content-sha256") == "" {
			w.WriteHeader(400)
			return
		}
		switch r.Method {
		case http.MethodPut:
			if r.URL.Path != "/bucket/prefix/backup.json" {
				t.Errorf("path = %q", r.URL.Path)
			}
			stored, _ = io.ReadAll(r.Body)
			w.WriteHeader(200)
		case http.MethodGet:
			if stored == nil {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write(stored)
		}
	}))
	defer srv.Close()

	cfg := &SyncS3Config{Bucket: "bucket", Prefix: "/prefix/", Endpoint: srv.URL, AccessKey: "AK", SecretKey: "SK", PathStyle: true}
	remote, err := s3PutObject(cfg, s3Key(cfg, "backup.json"), []byte("hello"))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if !strings.Contains(remote, "/bucket/prefix/backup.json") {
		t.Errorf("remote = %q", remote)
	}
	got, _, err := s3GetObject(cfg, s3Key(cfg, "backup.json"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("get = %q, %v", got, err)
	}
}

func TestGitHubPutGetRoundTrip(t *testing.T) {
	oldBase := githubAPIBase
	defer func() { githubAPIBase = oldBase }()
	var stored []byte
	var sha string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/contents/dir/backup.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		switch r.Method {
		case http.MethodGet:
			if r.Header.Get("Accept") == "application/vnd.github.raw" {
				_, _ = w.Write(stored)
				return
			}
			if stored == nil {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": sha})
		case http.MethodPut:
			var body struct {
				Content string `json:"content"`
				SHA     string `json:"sha"`
				Branch  string `json:"branch"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.SHA != sha {
				t.Errorf("更新未带旧 sha: %+v", body)
			}
			stored, _ = base64.StdEncoding.DecodeString(body.Content)
			sha = "new-sha"
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"commit":{"sha":"c"}}`))
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	githubAPIBase = srv.URL

	cfg := &SyncGitHubConfig{Repo: "owner/repo", Path: "dir", Branch: "main", Token: "tok"}
	if _, err := githubPutObject(cfg, "backup.json", []byte("v1")); err != nil {
		t.Fatalf("first put: %v", err)
	}
	if _, err := githubPutObject(cfg, "backup.json", []byte("v2")); err != nil {
		t.Fatalf("second put（应带 sha）: %v", err)
	}
	got, remote, err := githubGetObject(cfg, "backup.json")
	if err != nil || string(got) != "v2" {
		t.Fatalf("get = %q, %v", got, err)
	}
	if !strings.Contains(remote, "github://owner/repo/dir/backup.json") {
		t.Errorf("remote = %q", remote)
	}
}

// davsync 全链路：保存 S3 目标 + 口令 → 推送（密文）→ 还原。
func TestDavSyncS3SealedEndToEnd(t *testing.T) {
	var stored []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			stored, _ = io.ReadAll(r.Body)
		case http.MethodGet:
			if stored == nil {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write(stored)
		}
	}))
	defer srv.Close()

	g := &Gateway{
		settingsPath: filepath.Join(t.TempDir(), "settings.json"),
		models:       NewModelRouter(),
	}
	g.settings = runtimeSettings{ModelPrices: map[string]ModelPrice{"m": {InputPerM: 1}}}

	// 保存 S3 目标（path style 指向假服务）+ 口令
	body := `{"kind":"s3","url":"s3://bucket/backups","user":"AK","password":"SK","endpoint":"` + srv.URL + `","region":"us-east-1","pathStyle":true,"passphrase":"open sesame"}`
	req := httptest.NewRequest("POST", "/api/davsync/save", strings.NewReader(body))
	rec := httptest.NewRecorder()
	g.handleDavSyncSave(rec, req)
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "SK") || strings.Contains(rec.Body.String(), "open sesame") {
		t.Fatal("视图泄露了 secret/passphrase")
	}
	var view map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view["on"] != true || view["kind"] != "s3" || view["passphraseSet"] != true || view["passwordSet"] != true {
		t.Fatalf("view = %+v", view)
	}

	// 推送：远端必须是密封的
	rec = httptest.NewRecorder()
	g.handleDavSyncNow(rec, httptest.NewRequest("POST", "/api/davsync/now", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"remote"`) {
		t.Fatalf("now: %d %s", rec.Code, rec.Body.String())
	}
	if !sealedBackup(stored) || bytes.Contains(stored, []byte("model_prices")) {
		t.Fatal("远端备份应为口令密封的密文")
	}

	// 本机改成别的价格，再还原：应回到推送时的设置
	g.settingsMu.Lock()
	g.settings.ModelPrices = map[string]ModelPrice{"other": {InputPerM: 9}}
	g.settingsMu.Unlock()
	rec = httptest.NewRecorder()
	g.handleDavSyncRestore(rec, httptest.NewRequest("POST", "/api/davsync/restore", nil))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	g.settingsMu.Lock()
	_, restored := g.settings.ModelPrices["m"]
	g.settingsMu.Unlock()
	if !restored {
		t.Fatal("还原后价格表没有回来")
	}
}

func TestDavSyncSaveValidatesTargets(t *testing.T) {
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	cases := []string{
		`{"kind":"s3","url":"https://not-s3","user":"AK","password":"SK"}`,
		`{"kind":"s3","url":"s3://bucket","user":"AK"}`,
		`{"kind":"github","url":"github://onlyowner"}`,
		`{"kind":"github","url":"github://owner/repo","password":""}`,
		`{"kind":"webdav","url":"ftp://x"}`,
	}
	for _, body := range cases {
		rec := httptest.NewRecorder()
		g.handleDavSyncSave(rec, httptest.NewRequest("POST", "/api/davsync/save", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s -> %d %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestSettingsMasksSyncSecrets(t *testing.T) {
	enc, err := encryptPassphrase("open sesame")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}
	g.settings = runtimeSettings{Sync: &SyncConfig{
		Kind: "s3", PassphraseEnc: enc,
		S3: &SyncS3Config{Bucket: "b", AccessKey: "AK", SecretKey: "SUPERSECRET"},
	}}
	rec := httptest.NewRecorder()
	g.handleSettings(rec, httptest.NewRequest("GET", "/api/settings", nil))
	body := rec.Body.String()
	if strings.Contains(body, "SUPERSECRET") {
		t.Fatal("GET /api/settings 泄露了 S3 Secret")
	}
	if strings.Contains(body, enc) || strings.Contains(body, "open sesame") {
		t.Fatal("GET /api/settings 泄露了口令")
	}
	if !strings.Contains(body, `"secret_key":"******"`) {
		t.Fatalf("Secret 应为打码占位符: %s", body)
	}
}
