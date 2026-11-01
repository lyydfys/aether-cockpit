package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionsWritesAreReadOnly(t *testing.T) {
	g := New(Config{SettingsPath: t.TempDir() + "/settings.json", UsagePath: "-"})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/delete", g.handleSessionsWriteReadOnly)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/sessions/delete", strings.NewReader(`{"ids":["a"]}`)))
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), "只读") {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body.String())
	}
}
