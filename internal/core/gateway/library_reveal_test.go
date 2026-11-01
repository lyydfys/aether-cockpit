package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryReveal(t *testing.T) {
	orig := revealPathOpener
	t.Cleanup(func() { revealPathOpener = orig })

	var gotPath string
	var gotDir bool
	revealPathOpener = func(path string, isDir bool) error {
		gotPath, gotDir = path, isDir
		return nil
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{}
	do := func(body, remote string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/library/reveal", strings.NewReader(body))
		if remote != "" {
			req.Header.Set("X-Forwarded-For", remote)
		}
		rec := httptest.NewRecorder()
		g.handleLibraryReveal(rec, req)
		return rec
	}

	if rec := do(`{"path":"`+strings.ReplaceAll(dir, `\`, `\\`)+`"}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("dir: status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !gotDir || gotPath != dir {
		t.Errorf("dir opener got %q dir=%v", gotPath, gotDir)
	}
	if rec := do(`{"path":"`+strings.ReplaceAll(file, `\`, `\\`)+`"}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("file: status = %d", rec.Code)
	}
	if gotDir {
		t.Errorf("file reveal reported isDir=true")
	}
	if rec := do(`{"path":""}`, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("empty path = %d", rec.Code)
	}
	missing := filepath.Join(dir, "nope")
	if rec := do(`{"path":"`+strings.ReplaceAll(missing, `\`, `\\`)+`"}`, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("missing path = %d", rec.Code)
	}
	if rec := do(`{"path":"`+strings.ReplaceAll(dir, `\`, `\\`)+`"}`, "1.2.3.4"); rec.Code != http.StatusForbidden {
		t.Errorf("remote request = %d", rec.Code)
	}
}
