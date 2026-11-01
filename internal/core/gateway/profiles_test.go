package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfilesSaveListDelete(t *testing.T) {
	g := &Gateway{settingsPath: filepath.Join(t.TempDir(), "settings.json")}

	body := `{"name":"省钱档","clients":[{"client_id":"claude_code","model":"qoder-cn/auto"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/profiles", strings.NewReader(body))
	rec := httptest.NewRecorder()
	g.handleSaveProfile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存失败: %d %s", rec.Code, rec.Body.String())
	}
	var saved struct {
		Profile Profile `json:"profile"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &saved)
	if saved.Profile.ID == "" || saved.Profile.Name != "省钱档" || len(saved.Profile.Clients) != 1 {
		t.Fatalf("档案内容不对: %+v", saved.Profile)
	}

	// 同名再存 = 覆盖而不是新增。
	rec = httptest.NewRecorder()
	g.handleSaveProfile(rec, httptest.NewRequest(http.MethodPost, "/api/profiles", strings.NewReader(body)))
	if !strings.Contains(rec.Body.String(), `"replaced":true`) {
		t.Fatalf("同名档案应覆盖: %s", rec.Body.String())
	}

	// 未知客户端应被拒绝。
	rec = httptest.NewRecorder()
	g.handleSaveProfile(rec, httptest.NewRequest(http.MethodPost, "/api/profiles",
		strings.NewReader(`{"name":"坏档案","clients":[{"client_id":"nope"}]}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知客户端应 400，got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	g.handleProfiles(rec, httptest.NewRequest(http.MethodGet, "/api/profiles", nil))
	if !strings.Contains(rec.Body.String(), "省钱档") {
		t.Fatalf("列表缺少档案: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "坏档案") {
		t.Fatalf("被拒绝的档案不应入库: %s", rec.Body.String())
	}

	// 逐条删除。
	req = httptest.NewRequest(http.MethodDelete, "/api/profiles/"+saved.Profile.ID, nil)
	req.SetPathValue("id", saved.Profile.ID)
	rec = httptest.NewRecorder()
	g.handleDeleteProfile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("删除失败: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	g.handleProfiles(rec, httptest.NewRequest(http.MethodGet, "/api/profiles", nil))
	if strings.Contains(rec.Body.String(), "省钱档") {
		t.Fatalf("删除后仍存在: %s", rec.Body.String())
	}
}
