package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSigninCompatIsActionable(t *testing.T) {
	g := &Gateway{}
	rec := httptest.NewRecorder()
	g.handleSigninCompat(rec, httptest.NewRequest(http.MethodPost, "/api/signin", strings.NewReader(`{"agent":"antigravity"}`)))
	var out struct {
		Agent string `json:"agent"`
		State string `json:"state"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out.State != "failed" || out.Agent != "antigravity" || !strings.Contains(out.Error, "导入本机登录态") {
		t.Fatalf("out = %+v code=%d", out, rec.Code)
	}

	rec = httptest.NewRecorder()
	g.handleSigninImportCompat(rec, httptest.NewRequest(http.MethodPost, "/api/signin/import", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "导入本机配置") {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
}
