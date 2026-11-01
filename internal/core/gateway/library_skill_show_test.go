package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"aether-cockpit/internal/core/library"
)

func TestLibrarySkillShow(t *testing.T) {
	g, _, _ := agentApplyTestGateway(t)
	if _, err := g.library.SaveSkill(library.SkillEntry{
		Name: "sk", Description: "d", Content: "---\nname: sk\ndescription: d\n---\nbody",
	}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/library/skill", g.handleLibrarySkillShow)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/library/skill?name="+url.QueryEscape("sk"), nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "body") {
		t.Fatalf("show = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/library/skill?name=nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing = %d", rec.Code)
	}
}

func TestRTKUnsupportedExplains(t *testing.T) {
	g, _, _ := agentApplyTestGateway(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/library/rtk/install", g.handleRTKUnsupported)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/library/rtk/install", strings.NewReader("{}")))
	if rec.Code != http.StatusNotImplemented || !strings.Contains(rec.Body.String(), "RTK") {
		t.Fatalf("rtk install = %d %s", rec.Code, rec.Body.String())
	}
}
