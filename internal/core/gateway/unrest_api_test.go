package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aether-cockpit/internal/provider"
)

func TestRestOfStates(t *testing.T) {
	env := provider.NewAccountEnv("rest-acc", "mock-native")
	if restOf(env) != nil {
		t.Fatal("可用账号不应显示休息")
	}

	env.MarkCoolingDown(2*time.Minute, "429 Too Many Requests")
	rest := restOf(env)
	if rest == nil || rest.Why != "rate" || rest.Key != "rest-acc" {
		t.Fatalf("冷却态 rest = %#v", rest)
	}
	if d := time.Until(time.UnixMilli(rest.Until)); d < time.Minute || d > 3*time.Minute {
		t.Fatalf("until 不在两分钟附近: %v", d)
	}

	env.MarkDepleted(time.Now().Add(time.Hour), "额度已用尽")
	if rest = restOf(env); rest == nil || rest.Why != "quota" {
		t.Fatalf("耗尽态 rest = %#v", rest)
	}

	// 时间已过：不再显示（availableLocked 会在下一次判断时自动恢复）。
	env.RestoreState(provider.StateCoolingDown, "old", time.Now().Add(-time.Minute))
	if restOf(env) != nil {
		t.Fatal("过期的冷却不应显示")
	}

	env.MarkReady()
	if restOf(env) != nil {
		t.Fatal("MarkReady 后不应显示休息")
	}
}

func TestProvidersKeyRestAndUnrest(t *testing.T) {
	g := New(Config{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), UsagePath: "-"})
	env := provider.NewAccountEnv("rest-acc", "mock-native")
	env.Label = "sandbox"
	env.MarkCoolingDown(5*time.Minute, "429 Too Many Requests")
	g.pool.Add(env)

	rec := httptest.NewRecorder()
	g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("providers = %d %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("providers 非 JSON: %v", err)
	}
	views := envelope.Providers
	var rest map[string]any
	for _, v := range views {
		if v["id"] != "mock-native" {
			continue
		}
		keys, _ := v["keyList"].([]any)
		if len(keys) != 1 {
			t.Fatalf("keyList = %#v", v["keyList"])
		}
		rest, _ = keys[0].(map[string]any)["rest"].(map[string]any)
	}
	if rest == nil || rest["why"] != "rate" || rest["key"] != "rest-acc" {
		t.Fatalf("keyList rest = %#v", rest)
	}
	if until, _ := rest["until"].(float64); until <= float64(time.Now().UnixMilli()) {
		t.Fatalf("until 应在将来: %v", rest["until"])
	}

	// 手动解除后：立即可用，providers 里不再有 rest。
	code, out := redactPost(t, g.handleGatewayUnrest, `{"key":"rest-acc"}`)
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("unrest = %d %v", code, out)
	}
	if !env.Available() {
		t.Fatal("解除后账号应可用")
	}
	rec = httptest.NewRecorder()
	g.handleProviders(rec, httptest.NewRequest(http.MethodGet, "/api/providers", nil))
	if strings.Contains(rec.Body.String(), `"rest"`) {
		t.Fatalf("解除后仍有 rest: %s", rec.Body.String())
	}

	if code, _ := redactPost(t, g.handleGatewayUnrest, `{"key":""}`); code != http.StatusBadRequest {
		t.Fatalf("缺 key 应 400，得到 %d", code)
	}
	if code, _ := redactPost(t, g.handleGatewayUnrest, `{"key":"nope"}`); code != http.StatusNotFound {
		t.Fatalf("未知账号应 404，得到 %d", code)
	}
}

func TestGatewayUnrestAfter429(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "120")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
	}))
	defer upstream.Close()

	g := New(Config{
		MaxAttempts:      1,
		FirstByteTimeout: 5 * time.Second,
		SettingsPath:     filepath.Join(t.TempDir(), "settings.json"),
		UsagePath:        "-",
	})
	env := provider.NewAccountEnv("cool-acc", "openai-compat")
	env.Label = "cool"
	env.Credentials["base_url"] = upstream.URL
	env.Credentials["api_key"] = "fake"
	env.NetworkPolicy = provider.NetPolicyDirectForce
	env.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	g.pool.Add(env)

	srv := httptest.NewServer(g)
	defer srv.Close()

	chat := func() int {
		body := `{"model":"openai-compat/deepseek-chat","messages":[{"role":"user","content":"hi"}]}`
		resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := chat(); code != http.StatusTooManyRequests {
		t.Fatalf("上游 429 应原样回给下游 429，得到 %d", code)
	}
	st, _, resetAt := env.StateDetail()
	if st != provider.StateCoolingDown || !resetAt.After(time.Now()) {
		t.Fatalf("429 后应进入冷却: %s %v", st, resetAt)
	}
	if rest := restOf(env); rest == nil || rest.Why != "rate" {
		t.Fatalf("restOf = %#v", rest)
	}
	if hits.Load() != 1 {
		t.Fatalf("上游应被调用 1 次，实际 %d", hits.Load())
	}

	// 解除后下一次请求真的再问上游一次。
	if code, out := redactPost(t, g.handleGatewayUnrest, `{"key":"cool-acc"}`); code != http.StatusOK {
		t.Fatalf("unrest = %d %v", code, out)
	}
	if code := chat(); code != http.StatusTooManyRequests {
		t.Fatalf("解除后应再次请求上游，得到 %d", code)
	}
	if hits.Load() != 2 {
		t.Fatalf("解除后上游应被再调用一次，实际 %d", hits.Load())
	}
	// 再被拒绝就照常重新休息。
	if st, _, _ := env.StateDetail(); st != provider.StateCoolingDown {
		t.Fatalf("再次 429 后应重新冷却: %s", st)
	}
}
