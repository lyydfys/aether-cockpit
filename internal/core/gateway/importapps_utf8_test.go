package gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 非 UTF-8 的请求体（例如从 GBK 终端发来的中文名）以前会被 json 解码成
// U+FFFD 并静默写进账号标签；现在必须 400 说清楚。
func TestImportAppsRejectsInvalidUTF8(t *testing.T) {
	g, _, _ := agentApplyTestGateway(t)

	bad := []byte("{\"picks\":[{\"source\":\"qoder-intl\",\"ref\":\"0\",\"name\":\"\xbb\xf4\"}]}")
	rec := httptest.NewRecorder()
	g.handleImportAppsSubmit(rec, httptest.NewRequest(http.MethodPost, "/api/importapps", bytes.NewReader(bad)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "UTF-8") {
		t.Fatalf("非法 UTF-8 应 400 并说明：%d %s", rec.Code, rec.Body.String())
	}

	// 合法 UTF-8 不被这条规则拦下（未知 source 走"没有可导入项"的路径）。
	rec = httptest.NewRecorder()
	g.handleImportAppsSubmit(rec, httptest.NewRequest(http.MethodPost, "/api/importapps",
		strings.NewReader(`{"picks":[{"source":"nope","ref":"0","name":"名字"}]}`)))
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("合法 UTF-8 不应被拒：%d %s", rec.Code, rec.Body.String())
	}
}
