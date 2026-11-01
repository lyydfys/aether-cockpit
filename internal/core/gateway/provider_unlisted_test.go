package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func unlistedGateway(t *testing.T) *Gateway {
	t.Helper()
	g := New(Config{
		MaxAttempts:             2,
		FirstByteTimeout:        3 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:              10,
	})
	env := provider.NewAccountEnv("acc-mock", "mock-native")
	g.Pool().Add(env)
	g.models.Rebuild()
	return g
}

func modelListIDs(t *testing.T, srv *httptest.Server, path string) []string {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("解析 %s: %v", path, err)
	}
	out := make([]string, 0, len(body.Data))
	for _, d := range body.Data {
		out = append(out, d.ID)
	}
	return out
}

// unlisted：保存 / 回显；勾选后模型从 /v1/models 与 /v1beta/models 消失，
// 但显式请求仍可用（只是不再被推荐）。
func TestProviderUnlistedHidesFromModelList(t *testing.T) {
	g := unlistedGateway(t)
	srv := httptest.NewServer(g)
	defer srv.Close()

	find := func(list []string, want string) bool {
		for _, id := range list {
			if id == want {
				return true
			}
		}
		return false
	}
	if ids := modelListIDs(t, srv, "/v1/models"); !find(ids, "mock-native/echo-pro") {
		t.Fatalf("未勾选时应列出 mock-native/echo-pro: %v", ids)
	}

	if rec := saveHeaders(t, g, `{"id":"mock-native","unlisted":true}`); rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	if v := providerViewOf(t, g, "mock-native")["unlisted"]; v != true {
		t.Fatalf("回显 unlisted = %v", v)
	}
	if ids := modelListIDs(t, srv, "/v1/models"); find(ids, "mock-native/echo-pro") {
		t.Fatalf("勾选后不应出现在 /v1/models: %v", ids)
	}
	if ids := modelListIDs(t, srv, "/v1beta/models"); find(ids, "mock-native/echo-pro") {
		t.Fatalf("勾选后不应出现在 /v1beta/models: %v", ids)
	}

	// 显式请求仍然可达。
	body := `{"model":"mock-native/echo-pro","messages":[{"role":"user","content":"hi"}],"stream":false}`
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("显式请求应仍可用，status=%d: %s", resp.StatusCode, raw)
	}

	// 取消勾选后回到列表。
	if rec := saveHeaders(t, g, `{"id":"mock-native","unlisted":false}`); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d", rec.Code)
	}
	if ids := modelListIDs(t, srv, "/v1/models"); !find(ids, "mock-native/echo-pro") {
		t.Fatalf("取消后应重新列出: %v", ids)
	}
}
