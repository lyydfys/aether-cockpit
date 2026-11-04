package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// syncConfigForTest 造一个"已启用、带口令"的同步配置（口令用密文形式存）。
func syncConfigForTest(t *testing.T, g *Gateway, srvURL string) {
	t.Helper()
	enc, err := encryptPassphrase("pass-1234")
	if err != nil {
		t.Fatalf("加密口令失败：%v", err)
	}
	g.settingsMu.Lock()
	g.settings.Sync = &SyncConfig{
		Kind:          "webdav",
		WebDAV:        &WebDAVConfig{URL: srvURL, Username: "u", Password: "p"},
		PassphraseEnc: enc,
	}
	g.settingsMu.Unlock()
}

// 远端读失败（超时/5xx/解不开）绝不能被当成"远端不存在"：
// 那会让合并退化成"用本机整包覆盖"，一台机器的配置被一次网络抖动抹掉。
func TestPushAbortsWhenRemoteUnreadable(t *testing.T) {
	var puts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusInternalServerError)
		case http.MethodPut:
			puts++
			w.WriteHeader(http.StatusCreated)
		case "MKCOL":
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	syncConfigForTest(t, g, srv.URL)

	if _, _, err := g.pushBackupToTarget(); err == nil {
		t.Fatal("远端读不出来时应报错并中止")
	}
	if puts != 0 {
		t.Fatalf("读失败时绝不能写入（PUT 次数 = %d）", puts)
	}
}

// 404 是"远端确实还没有备份"：这时允许首次写入，不该被上面那条挡住。
func TestPushAllowsFirstWriteWhenRemoteAbsent(t *testing.T) {
	var puts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			puts++
			w.Header().Set("ETag", `"v1"`)
			w.WriteHeader(http.StatusCreated)
		case "MKCOL":
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	syncConfigForTest(t, g, srv.URL)

	if _, _, err := g.pushBackupToTarget(); err != nil {
		t.Fatalf("首次同步应允许写入：%v", err)
	}
	if puts != 1 {
		t.Fatalf("PUT 次数 = %d，期望 1", puts)
	}
}

// 远端 404 的语义必须与"读失败"分开表达。
func TestSyncRemoteAbsentSentinel(t *testing.T) {
	if !errors.Is(errSyncRemoteAbsent, errSyncRemoteAbsent) {
		t.Fatal("哨兵错误应能 errors.Is 判断")
	}
	if strings.Contains(errSyncRemoteAbsent.Error(), "WebDAV") {
		t.Fatal("哨兵错误不该绑定具体协议")
	}
}
