package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"aether-cockpit/internal/core/library"
)

// 插件的 change() 会把响应整体当成库视图；所有动作必须返回这套字段。
func TestLibraryViewShape(t *testing.T) {
	dir := t.TempDir()
	store := library.NewStoreWithHome(filepath.Join(dir, "library.json"), t.TempDir())
	g := &Gateway{library: store}
	rec := httptest.NewRecorder()
	g.writeLibraryView(rec)
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"skills", "servers", "hooks", "agents", "projects", "backups", "hookAgents", "instructions"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("视图缺少 %s: %v", k, m)
		}
	}
}
