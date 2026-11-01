package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenEndpoint(t *testing.T) {
	orig := openExternal
	t.Cleanup(func() { openExternal = orig })

	var got string
	openExternal = func(raw string) error {
		got = raw
		return nil
	}
	g := &Gateway{}
	do := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/open", strings.NewReader(body))
		rec := httptest.NewRecorder()
		g.handleOpen(rec, req)
		return rec
	}

	rec := do(`{"url":"https://example.com/status?x=1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid url status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got != "https://example.com/status?x=1" {
		t.Fatalf("opened %q", got)
	}

	for _, bad := range []string{
		`{"url":"ftp://example.com/file"}`,
		`{"url":"javascript:alert(1)"}`,
		`{"url":"file:///C:/Windows/System32/calc.exe"}`,
		`{"url":"https://"}`,
		`{"url":""}`,
		`{`,
	} {
		if rec := do(bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d want 400", bad, rec.Code)
		}
	}
	if got != "https://example.com/status?x=1" {
		t.Fatalf("opener called for rejected url: %q", got)
	}

	openExternal = func(string) error { return errors.New("no opener") }
	if rec := do(`{"url":"http://example.com"}`); rec.Code != http.StatusBadGateway {
		t.Fatalf("opener failure status = %d want 502", rec.Code)
	}
}
