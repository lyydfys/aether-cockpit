package gateway

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 处理器 panic 必须先写进日志（产品日志会落盘），再交给 net/http 断连。
func TestPanicIsLogged(t *testing.T) {
	g := New(Config{
		MaxAttempts:      1,
		FirstByteTimeout: time.Second,
		StateTTL:         time.Minute,
		MaxRecords:       10,
		SettingsPath:     t.TempDir() + "/settings.json",
	})
	g.mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) { panic("boom-for-test") })

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	srv := httptest.NewServer(g)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/boom")
	if err == nil {
		resp.Body.Close()
	}

	out := buf.String()
	if !strings.Contains(out, "[PANIC]") || !strings.Contains(out, "boom-for-test") {
		t.Fatalf("panic 未写入日志: %q", out)
	}
	if !strings.Contains(out, "panic_test.go") && !strings.Contains(out, "goroutine") {
		t.Fatalf("日志缺少堆栈: %q", out)
	}
}
