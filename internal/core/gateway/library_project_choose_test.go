package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectChooseHandler(t *testing.T) {
	g, _, _ := agentApplyTestGateway(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/library/projects/choose", g.handleProjectChoose)

	call := func() (*httptest.ResponseRecorder, map[string]any) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/library/projects/choose", strings.NewReader("{}")))
		out := map[string]any{}
		if rec.Code == http.StatusOK {
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
		}
		return rec, out
	}

	g.pickFolder = func(context.Context) (string, error) { return `C:\code\app`, nil }
	rec, out := call()
	if rec.Code != http.StatusOK || out["dir"] != `C:\code\app` {
		t.Fatalf("choose = %d %v", rec.Code, out)
	}

	// 取消：空串，页面保持手输。
	g.pickFolder = func(context.Context) (string, error) { return "", nil }
	rec, out = call()
	if rec.Code != http.StatusOK || out["dir"] != "" {
		t.Fatalf("cancel = %d %v", rec.Code, out)
	}

	g.pickFolder = func(context.Context) (string, error) { return "", errors.New("对话框失败") }
	rec, _ = call()
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "目录选择器") {
		t.Fatalf("error = %d %s", rec.Code, rec.Body.String())
	}
}
