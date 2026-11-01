package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func lengthsGateway(t *testing.T) *Gateway {
	t.Helper()
	g := New(Config{
		MaxAttempts:             2,
		FirstByteTimeout:        3 * time.Second,
		MaxConcurrentPerAccount: -1,
		SettingsPath:            filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath:        filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:              10,
	})
	env := provider.NewAccountEnv("len-acc", "mock-native")
	g.Pool().Add(env)
	g.models.Rebuild()
	return g
}

func geminiModel(t *testing.T, srv *httptest.Server, id string) map[string]any {
	t.Helper()
	resp, err := http.Get(srv.URL + "/v1beta/models/" + id)
	if err != nil {
		t.Fatalf("GET gemini model: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d: %s", resp.StatusCode, raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("解析: %v", err)
	}
	return out
}

// contexts/outputs/compacts：保存 → 文本回显 → Gemini 模型元信息生效 → 清空恢复。
func TestProviderLengthsSaveEchoAndGemini(t *testing.T) {
	g := lengthsGateway(t)
	srv := httptest.NewServer(g)
	defer srv.Close()

	base := geminiModel(t, srv, "mock-native/echo-pro")
	baseIn := base["inputTokenLimit"]

	rec := saveHeaders(t, g, `{"id":"mock-native","contexts":{"*":1000000,"gpt-6":128000},"outputs":{"*":4096},"compacts":{"*":800000}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d: %s", rec.Code, rec.Body.String())
	}
	view := providerViewOf(t, g, "mock-native")
	cx, _ := view["contexts"].(map[string]any)
	if cx["*"] != float64(1000000) || cx["gpt-6"] != float64(128000) {
		t.Fatalf("contexts 回显 = %v", view["contexts"])
	}
	ox, _ := view["outputs"].(map[string]any)
	cpx, _ := view["compacts"].(map[string]any)
	if ox["*"] != float64(4096) || cpx["*"] != float64(800000) {
		t.Fatalf("outputs/compacts 回显 = %v / %v", view["outputs"], view["compacts"])
	}

	got := geminiModel(t, srv, "mock-native/echo-pro")
	if got["inputTokenLimit"] != float64(1000000) {
		t.Fatalf("inputTokenLimit = %v，期望 1000000（* 覆盖）", got["inputTokenLimit"])
	}
	if got["outputTokenLimit"] != float64(4096) {
		t.Fatalf("outputTokenLimit = %v，期望 4096", got["outputTokenLimit"])
	}

	// 模型行带上 max/context（编辑器"每个模型的最大窗口"用）。
	models := view["models"].([]any)
	found := false
	for _, m := range models {
		mm := m.(map[string]any)
		if mm["id"] == "echo-pro" {
			found = true
			if mm["max"] != float64(1000000) {
				t.Fatalf("模型行 max = %v，期望 1000000", mm["max"])
			}
		}
	}
	if !found {
		t.Fatalf("models 里没有 echo-pro: %v", models)
	}

	// 清空三张表：回显为空、Gemini 元信息回到 manifest。
	if rec := saveHeaders(t, g, `{"id":"mock-native","contexts":{},"outputs":{},"compacts":{}}`); rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d: %s", rec.Code, rec.Body.String())
	}
	view = providerViewOf(t, g, "mock-native")
	if view["contexts"] != nil || view["outputs"] != nil || view["compacts"] != nil {
		t.Fatalf("清空后仍有回显: %v / %v / %v", view["contexts"], view["outputs"], view["compacts"])
	}
	got = geminiModel(t, srv, "mock-native/echo-pro")
	if got["inputTokenLimit"] != baseIn {
		t.Fatalf("清空后 inputTokenLimit = %v，期望回到 %v", got["inputTokenLimit"], baseIn)
	}
	if _, ok := got["outputTokenLimit"]; ok {
		t.Fatalf("清空后不应有 outputTokenLimit: %v", got)
	}
}

// 非法长度要明确拒绝。
func TestProviderLengthsRejectInvalid(t *testing.T) {
	g := lengthsGateway(t)
	for _, body := range []string{
		`{"id":"mock-native","contexts":{"gpt-6":-1}}`,
		`{"id":"mock-native","contexts":{"gpt-6":20000000}}`,
	} {
		if rec := saveHeaders(t, g, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400，got %d", body, rec.Code)
		}
	}
}
