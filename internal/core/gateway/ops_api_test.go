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

func opsGateway(t *testing.T) *Gateway {
	t.Helper()
	return New(Config{
		Addr:             "127.0.0.1:0",
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		AccountStorePath: filepath.Join(t.TempDir(), "accounts.json"),
		MaxRecords:       10,
	})
}

// /healthz 是纯存活探针，不依赖登记模型。
func TestHealthzAlwaysOK(t *testing.T) {
	g := opsGateway(t)
	rec := httptest.NewRecorder()
	g.handleHealthz(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body, _ := io.ReadAll(rec.Body); string(body) != "ok\n" {
		t.Fatalf("body = %q", body)
	}
}

// /readyz：没有任何账号时未就绪（503），有账号后 200。
func TestReadyzTracksModels(t *testing.T) {
	g := opsGateway(t)
	read := func() (int, map[string]any) {
		rec := httptest.NewRecorder()
		g.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, out := read(); code != http.StatusServiceUnavailable || out["ready"] != false {
		t.Fatalf("没有账号应未就绪: code=%d out=%+v", code, out)
	}
	g.Pool().Add(provider.NewAccountEnv("ops-acc", "mock-native"))
	g.models.Rebuild()
	code, out := read()
	if code != http.StatusOK || out["ready"] != true {
		t.Fatalf("有账号后应就绪: code=%d out=%+v", code, out)
	}
	if out["models"] == float64(0) || out["accounts"] != float64(1) {
		t.Fatalf("计数不对: %+v", out)
	}
	// providers 与 /api/health 同义（已注册渠道）；在用渠道另算。
	if out["providers"] != float64(len(provider.All())) {
		t.Fatalf("providers 应是已注册渠道数（与 /api/health 一致）: %+v", out)
	}
	if out["providers_in_use"] != float64(1) {
		t.Fatalf("providers_in_use 应为 1: %+v", out)
	}
}

// /version 给出构建信息；/api/health 也带 version/revision。
func TestVersionEndpoint(t *testing.T) {
	g := opsGateway(t)
	rec := httptest.NewRecorder()
	g.handleVersion(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["version"] == "" || out["go"] == "" {
		t.Fatalf("version/go 缺失: %+v", out)
	}

	rec = httptest.NewRecorder()
	g.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d", rec.Code)
	}
	var h map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &h)
	if h["status"] != "ok" || h["version"] == nil {
		t.Fatalf("health 字段不全: %+v", h)
	}
}

// 就绪探针不该被账号冷却影响（网关允许先起服务再配置/等待额度）。
func TestReadyzIgnoresCoolingAccounts(t *testing.T) {
	g := opsGateway(t)
	env := provider.NewAccountEnv("ops-cool", "mock-native")
	g.Pool().Add(env)
	env.MarkCoolingDown(time.Minute, "429")
	g.models.Rebuild()
	rec := httptest.NewRecorder()
	g.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("冷却账号不应让网关未就绪: %d", rec.Code)
	}
}

// 全部账号凭证失效时必须判未就绪：在有人重新登录之前它接不了任何请求，
// 负载均衡把流量打过来只会得到 401。
func TestReadyzUnreadyWhenAllAccountsInvalid(t *testing.T) {
	g := opsGateway(t)
	env := provider.NewAccountEnv("ops-bad", "mock-native")
	g.Pool().Add(env)
	env.MarkInvalid("401 from upstream")
	g.models.Rebuild()
	rec := httptest.NewRecorder()
	g.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusServiceUnavailable || out["ready"] != false {
		t.Fatalf("全部失效应未就绪: code=%d out=%+v", rec.Code, out)
	}
	if out["usable_accounts"] != float64(0) || out["accounts"] != float64(1) {
		t.Fatalf("计数不对: %+v", out)
	}
	if s, _ := out["reasons"].([]any); len(s) == 0 || !strings.Contains(s[0].(string), "失效") {
		t.Fatalf("原因应说明凭证失效: %+v", out["reasons"])
	}
}
